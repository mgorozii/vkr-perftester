package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mgorozii/perftester/internal/app"
	"github.com/mgorozii/perftester/internal/domain"
)

type mockRepo struct {
	app.RunRepository
	runs     []domain.Run
	steps    map[string][]domain.SearchStep
	stepsErr error
}

func (m *mockRepo) CreateRun(_ context.Context, r domain.Run) error {
	m.runs = append(m.runs, r)
	return nil
}

func (m *mockRepo) GetRun(_ context.Context, id string) (domain.Run, error) {
	for _, r := range m.runs {
		if r.ID == id {
			return r, nil
		}
	}
	return domain.Run{}, app.ErrNotFound
}

func (m *mockRepo) ListRuns(_ context.Context, _ app.RunFilter) ([]domain.Run, error) {
	return m.runs, nil
}
func (m *mockRepo) DeleteRun(_ context.Context, _ string) error            { return nil }
func (m *mockRepo) SaveMetrics(_ context.Context, _ []domain.Metric) error { return nil }
func (m *mockRepo) SaveSearchSteps(_ context.Context, steps []domain.SearchStep) error {
	if m.steps == nil {
		m.steps = map[string][]domain.SearchStep{}
	}
	if len(steps) == 0 {
		return nil
	}
	m.steps[steps[0].RunID] = append(m.steps[steps[0].RunID], steps...)
	return nil
}

func (m *mockRepo) ListSearchSteps(_ context.Context, runID string) ([]domain.SearchStep, error) {
	if m.stepsErr != nil {
		return nil, m.stepsErr
	}
	return m.steps[runID], nil
}

func (m *mockRepo) ListMetrics(_ context.Context, _ app.MetricsFilter) ([]domain.Metric, error) {
	return nil, nil
}

func (m *mockRepo) UpdateRunStatus(_ context.Context, id string, status domain.Status, now time.Time, failureReason string) error {
	for i, r := range m.runs {
		if r.ID == id {
			m.runs[i].Status = status
			m.runs[i].FailureReason = failureReason
			m.runs[i].UpdatedAt = now
			return nil
		}
	}
	return app.ErrNotFound
}

func (m *mockRepo) ListActiveRuns(_ context.Context) ([]domain.Run, error) {
	return nil, nil
}

type logCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (l *logCapture) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (l *logCapture) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, r.Clone())
	return nil
}
func (l *logCapture) WithAttrs(_ []slog.Attr) slog.Handler { return l }
func (l *logCapture) WithGroup(_ string) slog.Handler      { return l }

type mockOrch struct{}

func (m *mockOrch) Submit(_ context.Context, _ domain.Run) error  { return nil }
func (m *mockOrch) Cleanup(_ context.Context, _ domain.Run) error { return nil }

func TestHealth(t *testing.T) {
	svc := app.NewService(slog.Default(), &mockRepo{}, &mockOrch{}, func() string { return "1" }, time.Now, time.Hour)
	srv := New(slog.Default(), svc, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/healthz", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if string(body) != `{"status":"ok"}` {
		t.Errorf("expected ok status, got %s", string(body))
	}
}

func TestStartTest(t *testing.T) {
	repo := &mockRepo{}
	svc := app.NewService(slog.Default(), repo, &mockOrch{}, func() string { return "run-1" }, time.Now, time.Hour)
	srv := New(slog.Default(), svc, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	payload := map[string]any{
		"tenant":     "acme",
		"name":       "resnet",
		"s3_path":    "s3://models/resnet",
		"target_rps": 10,
		"duration":   "15s",
		"protocol":   "HTTP",
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/api/v1/tests:start", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}

	var resp map[string]string
	_ = json.NewDecoder(res.Body).Decode(&resp)
	if resp["run_id"] != "run-1" {
		t.Errorf("expected run-1, got %s", resp["run_id"])
	}
}

func TestGrafanaReportHTML(t *testing.T) {
	repo := &mockRepo{}
	svc := app.NewService(slog.Default(), repo, &mockOrch{}, func() string { return "run-1" }, time.Now, time.Hour)

	rps := int32(10)
	dur := 15 * time.Second
	_, _ = svc.StartTest(context.Background(), domain.StartTestCmd{
		Tenant: "acme", Name: "resnet", S3Path: "s3://models/resnet", Protocol: "HTTP", TargetRPS: &rps, Duration: &dur,
	})

	srv := New(slog.Default(), svc, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/v1/grafana/report.html", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(body), "Please select filters in Grafana") {
		t.Errorf("expected selection prompt, got: %s", string(body))
	}

	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/v1/grafana/report.html?run_id=run-1", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ = io.ReadAll(res.Body)
	if !strings.Contains(string(body), "run-1") {
		t.Errorf("expected run-1 in report, got: %s", string(body))
	}
}

func TestGetRunIncludesFailureReasonAndStepError(t *testing.T) {
	repo := &mockRepo{
		runs: []domain.Run{{
			ID:            "run-1",
			Status:        domain.StatusFailed,
			FailureReason: "final validation at 15 RPS failed: signal: killed",
		}},
		steps: map[string][]domain.SearchStep{
			"run-1": {{
				RunID:      "run-1",
				StepNumber: 8,
				RPS:        15,
				StopReason: "executor_error",
				Error:      "signal: killed",
			}},
		},
	}
	svc := app.NewService(slog.Default(), repo, &mockOrch{}, func() string { return "run-1" }, time.Now, time.Hour)
	ts := httptest.NewServer(New(slog.Default(), svc, nil).Handler())
	defer ts.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/v1/runs/run-1", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["failure_reason"] != "final validation at 15 RPS failed: signal: killed" {
		t.Fatalf("failure_reason=%v", body["failure_reason"])
	}
	steps, ok := body["steps"].([]any)
	if !ok || len(steps) != 1 {
		t.Fatalf("steps=%v", body["steps"])
	}
	step, ok := steps[0].(map[string]any)
	if !ok || step["error"] != "signal: killed" {
		t.Fatalf("step=%v", steps[0])
	}
}

func TestGetRunLogsErrorWhenStepsQueryFails(t *testing.T) {
	repo := &mockRepo{
		runs:     []domain.Run{{ID: "run-1", Status: domain.StatusSuccess}},
		stepsErr: errors.New("db down"),
	}
	cap := &logCapture{}
	logger := slog.New(cap)
	svc := app.NewService(logger, repo, &mockOrch{}, func() string { return "run-1" }, time.Now, time.Hour)
	ts := httptest.NewServer(New(logger, svc, nil).Handler())
	defer ts.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/v1/runs/run-1", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200 (graceful degradation), got %d", res.StatusCode)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	for _, r := range cap.records {
		if r.Level == slog.LevelError && strings.Contains(r.Message, "list search steps") {
			return
		}
	}
	t.Error("expected error to be logged when ListSearchSteps fails")
}
