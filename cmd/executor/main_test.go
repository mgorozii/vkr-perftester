package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mgorozii/perftester/internal/config"
	"github.com/mgorozii/perftester/internal/k6"
)

func TestSummarizeUsesP99Fallbacks(t *testing.T) {
	summary := K6Summary{Metrics: map[string]map[string]float64{
		"http_req_duration": {"p(99)": 55},
		"http_req_failed":   {"value": 0.02},
	}}
	res := summarize(summary, 10)
	if res.p99 != 55 {
		t.Fatalf("p99=%v", res.p99)
	}
	if res.errorRate != 0.02 {
		t.Fatalf("error_rate=%v", res.errorRate)
	}
}

func TestSearchStopReasonUsesLatencyOrErrors(t *testing.T) {
	r := &Runner{}
	r.SLO = 100
	if got := searchStopReason(r, stepResult{p99: 101}); got != stopReasonLatency {
		t.Fatalf("got=%q", got)
	}
	if got := searchStopReason(r, stepResult{errorRate: 0.02}); got != stopReasonErrors {
		t.Fatalf("got=%q", got)
	}
	if got := searchStopReason(r, stepResult{p99: 99, errorRate: 0.001}); got != "" {
		t.Fatalf("got=%q", got)
	}
}

func TestFinalDurationPrefersConfiguredValue(t *testing.T) {
	if got := finalDuration(&Runner{BaseConfig: k6.Config{Duration: "2m"}}); got != 2*time.Minute {
		t.Fatalf("got=%s", got)
	}
	if got := finalDuration(&Runner{}); got != defaultFinalRun {
		t.Fatalf("got=%s", got)
	}
}

func TestFinalDurationUsesDefaultWhenInvalid(t *testing.T) {
	r := &Runner{
		Executor: config.Executor{},
		BaseConfig: k6.Config{
			Duration: "invalid",
		},
	}
	if got := finalDuration(r); got != defaultFinalRun {
		t.Fatalf("got=%s", got)
	}
}

func TestParseSummaryReturnsErrorOnInvalidJSON(t *testing.T) {
	if _, err := parseSummary([]byte("not json")); err == nil {
		t.Fatal("expected error for invalid k6 summary JSON")
	}
}

func TestParseSummaryParsesMetrics(t *testing.T) {
	data := []byte(`{"metrics":{"http_req_duration":{"p(99)":42},"http_req_failed":{"value":0.005}}}`)
	got, err := parseSummary(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metrics["http_req_duration"]["p(99)"] != 42 {
		t.Fatalf("p99=%v", got.Metrics["http_req_duration"]["p(99)"])
	}
}

func stubSequence(codes ...int) (*httptest.Server, *atomic.Int32) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(calls.Add(1)) - 1
		if n < len(codes) {
			w.WriteHeader(codes[n])
		} else {
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	return srv, &calls
}

func silentLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestSaveStepRetriesOn5xx(t *testing.T) {
	retryDelay = 0
	t.Cleanup(func() { retryDelay = time.Second })

	srv, calls := stubSequence(500, 500, 202)
	defer srv.Close()

	r := &Runner{Executor: config.Executor{StepsURL: srv.URL}}
	saveStep(context.Background(), srv.Client(), silentLogger(), r, SearchStep{StepNumber: 1})
	if got := calls.Load(); got < 3 {
		t.Fatalf("ожидалось ≥3 запросов (ретраи), получено %d", got)
	}
}

func TestSaveStepDoesNotRetryOn4xx(t *testing.T) {
	retryDelay = 0
	t.Cleanup(func() { retryDelay = time.Second })

	srv, calls := stubSequence(400)
	defer srv.Close()

	r := &Runner{Executor: config.Executor{StepsURL: srv.URL}}
	saveStep(context.Background(), srv.Client(), silentLogger(), r, SearchStep{StepNumber: 1})
	if got := calls.Load(); got != 1 {
		t.Fatalf("ожидался ровно 1 запрос (без ретрая на 4xx), получено %d", got)
	}
}

func TestReportFailureRetriesOn5xx(t *testing.T) {
	retryDelay = 0
	t.Cleanup(func() { retryDelay = time.Second })

	srv, calls := stubSequence(500, 500, 202)
	defer srv.Close()

	r := &Runner{Executor: config.Executor{StatusURL: srv.URL}}
	reportFailure(context.Background(), srv.Client(), silentLogger(), r, errors.New("test failure"))
	if got := calls.Load(); got < 3 {
		t.Fatalf("ожидалось ≥3 запросов (ретраи), получено %d", got)
	}
}
