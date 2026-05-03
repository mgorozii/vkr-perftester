package telemetry

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestMetricsEnabledWithCommonEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	if !metricsEnabled() {
		t.Fatal("metrics должны включаться через OTEL_EXPORTER_OTLP_ENDPOINT")
	}
}

func TestExtractEnvRestoresSpanContext(t *testing.T) {
	t.Setenv(EnvTraceparent, "00-0102030405060708090a0b0c0d0e0f10-0102030405060708-01")
	t.Setenv(EnvTracestate, "")
	t.Setenv(EnvBaggage, "")

	sc := trace.SpanContextFromContext(ExtractEnv(context.Background()))
	if !sc.IsValid() {
		t.Fatal("span context не извлечён из env")
	}
	if got := sc.TraceID().String(); got != "0102030405060708090a0b0c0d0e0f10" {
		t.Fatalf("trace_id=%s", got)
	}
}

func TestLoggerWritesTraceIDsToStdoutFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := NewTextLogger(&buf)
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		SpanID:     trace.SpanID{2, 2, 2, 2, 2, 2, 2, 2},
		TraceFlags: trace.FlagsSampled,
	}))

	logger.InfoContext(ctx, "hello")

	out := buf.String()
	if !strings.Contains(out, "trace_id=01010101010101010101010101010101") {
		t.Fatalf("trace_id отсутствует в логе: %s", out)
	}
	if !strings.Contains(out, "span_id=0202020202020202") {
		t.Fatalf("span_id отсутствует в логе: %s", out)
	}
	if !strings.Contains(out, "trace_flags=01") {
		t.Fatalf("trace_flags отсутствует в логе: %s", out)
	}
}

func TestInjectEnvProducesTraceparent(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
	}))

	env := InjectEnv(ctx)
	if env[EnvTraceparent] == "" {
		t.Fatal("traceparent не сгенерирован")
	}
}

func TestMain(m *testing.M) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	os.Exit(m.Run())
}
