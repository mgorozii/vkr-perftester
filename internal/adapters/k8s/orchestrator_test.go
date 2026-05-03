package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/mgorozii/perftester/internal/domain"
	"github.com/mgorozii/perftester/internal/telemetry"
)

type stubK8s struct {
	capturedConfigJSON string
	capturedEnv        map[string]string
	createJobErr       error
}

func (s *stubK8s) EnsureInference(_ context.Context, _ domain.Run, _ ResourceNames) error {
	return nil
}

func (s *stubK8s) CreateLoadJob(_ context.Context, _, _, configJSON string, env map[string]string) error {
	s.capturedConfigJSON = configJSON
	s.capturedEnv = env
	return s.createJobErr
}

func (s *stubK8s) DeleteNamespace(_ context.Context, _ string) error { return nil }

func newTestOrch(stub *stubK8s) *Orchestrator {
	return New(slog.New(slog.DiscardHandler), stub,
		"http://host/report", "http://inf", "grpc://inf", nil)
}

func testRun() domain.Run {
	return domain.Run{
		ID:        "run-1",
		Tenant:    "tenant",
		ModelName: "resnet",
		Protocol:  domain.ProtocolHTTP,
		Payload:   `{"input":[[1,2,3]]}`,
	}
}

func TestSubmitPassesValidConfigJSON(t *testing.T) {
	stub := &stubK8s{}
	if err := newTestOrch(stub).Submit(context.Background(), testRun()); err != nil {
		t.Fatal(err)
	}

	if stub.capturedConfigJSON == "" {
		t.Fatal("CreateLoadJob получил пустой configJSON")
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(stub.capturedConfigJSON), &cfg); err != nil {
		t.Fatalf("configJSON невалидный JSON: %v", err)
	}

	for _, key := range []string{"run_id", "model_name", "protocol", "payload"} {
		if _, ok := cfg[key]; !ok {
			t.Errorf("configJSON не содержит поле %q", key)
		}
	}
}

func TestSubmitPropagatesCreateJobError(t *testing.T) {
	stub := &stubK8s{createJobErr: errors.New("k8s unavailable")}
	err := newTestOrch(stub).Submit(context.Background(), testRun())
	if err == nil {
		t.Fatal("ожидалась ошибка, получил nil")
	}
}

func TestSubmitPropagatesTraceContext(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	stub := &stubK8s{}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	}))

	if err := newTestOrch(stub).Submit(ctx, testRun()); err != nil {
		t.Fatal(err)
	}
	if stub.capturedEnv[telemetry.EnvTraceparent] == "" {
		t.Fatal("traceparent не проброшен в env")
	}
}
