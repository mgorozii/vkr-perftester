package app_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/mgorozii/perftester/internal/adapters/memory"
	"github.com/mgorozii/perftester/internal/app"
	"github.com/mgorozii/perftester/internal/domain"
)

type orchStub struct {
	submitted []domain.Run
	cleaned   []domain.Run
}

func (o *orchStub) Submit(_ context.Context, run domain.Run) error {
	o.submitted = append(o.submitted, run)
	return nil
}

func (o *orchStub) Cleanup(_ context.Context, run domain.Run) error {
	o.cleaned = append(o.cleaned, run)
	return nil
}

//go:fix inline
func ptr[T any](v T) *T { return new(v) }

func TestStartTestPersistsPendingAndSubmits(t *testing.T) {
	repo := memory.NewRunRepo()
	orch := &orchStub{}
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	svc := app.NewService(slog.Default(), repo, orch, func() string { return "run-1" }, func() time.Time { return now }, time.Minute)

	id, err := svc.StartTest(context.Background(), domain.StartTestCmd{
		Tenant: "acme", Name: "resnet", S3Path: "s3://m/resnet", TargetRPS: new(int32(50)), Duration: ptr(time.Minute), Protocol: domain.ProtocolGRPC,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "run-1" {
		t.Fatalf("id=%s", id)
	}
	run, err := repo.GetRun(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.StatusRunning {
		t.Fatalf("status=%s", run.Status)
	}
	if len(orch.submitted) != 1 {
		t.Fatalf("submitted=%d", len(orch.submitted))
	}
}

func TestStartTestSearchMode(t *testing.T) {
	repo := memory.NewRunRepo()
	orch := &orchStub{}
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	svc := app.NewService(slog.Default(), repo, orch, func() string { return "run-1" }, func() time.Time { return now }, time.Minute)

	id, err := svc.StartTest(context.Background(), domain.StartTestCmd{
		Tenant: "acme", Name: "resnet", S3Path: "s3://m/resnet", MaxLatencyMS: new(int32(100)), Protocol: domain.ProtocolHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := repo.GetRun(context.Background(), id)
	if *run.MaxLatencyMS != 100 {
		t.Fatalf("max_latency=%d", *run.MaxLatencyMS)
	}
}

func TestAcceptReportStoresMetricsAndMarksSuccess(t *testing.T) {
	repo := memory.NewRunRepo()
	orch := &orchStub{}
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	svc := app.NewService(slog.Default(), repo, orch, func() string { return "run-1" }, func() time.Time { return now }, time.Minute)
	_, _ = svc.StartTest(context.Background(), domain.StartTestCmd{
		Tenant: "acme", Name: "resnet", S3Path: "s3://m/resnet", TargetRPS: new(int32(50)), Duration: ptr(time.Minute), Protocol: domain.ProtocolHTTP,
	})

	err := svc.AcceptReport(context.Background(), domain.Report{
		RunID:    "run-1",
		Received: now.Add(time.Minute),
		Metrics: []domain.MetricPoint{
			{MetricName: "p95", Value: 12.5},
			{MetricName: "rps", Value: 49.8},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := repo.GetRun(context.Background(), "run-1")
	if run.Status != domain.StatusSuccess {
		t.Fatalf("status=%s", run.Status)
	}
	if len(orch.cleaned) != 1 {
		t.Fatalf("cleaned=%d", len(orch.cleaned))
	}
	metrics, _ := repo.ListMetrics(context.Background(), app.MetricsFilter{RunID: "run-1"})
	if len(metrics) != 2 {
		t.Fatalf("metrics=%d", len(metrics))
	}
}

func TestTimeoutSweepCleansAndFailsTimedOutRuns(t *testing.T) {
	repo := memory.NewRunRepo()
	orch := &orchStub{}
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	svc := app.NewService(slog.Default(), repo, orch, func() string { return "run-1" }, func() time.Time { return now }, 30*time.Second)

	createdAt := now.Add(-12 * time.Minute)

	run := domain.Run{
		ID:        "run-1",
		Tenant:    "acme",
		ModelName: "resnet",
		TargetRPS: new(int32(50)),
		Duration:  ptr(time.Minute),
		Protocol:  domain.ProtocolHTTP,
		Status:    domain.StatusRunning,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}

	if err := repo.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	if err := svc.TimeoutSweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	updatedRun, _ := repo.GetRun(context.Background(), "run-1")
	if updatedRun.Status != domain.StatusFailed {
		t.Fatalf("status=%s", updatedRun.Status)
	}
	if updatedRun.FailureReason == "" {
		t.Fatal("missing failure reason")
	}
	if len(orch.cleaned) != 1 {
		t.Fatalf("orch not cleaned")
	}
}

func TestFailRunStoresReason(t *testing.T) {
	repo := memory.NewRunRepo()
	orch := &orchStub{}
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	svc := app.NewService(slog.Default(), repo, orch, func() string { return "run-1" }, func() time.Time { return now }, time.Minute)
	_, _ = svc.StartTest(context.Background(), domain.StartTestCmd{
		Tenant: "acme", Name: "resnet", S3Path: "s3://m/resnet", MaxLatencyMS: new(int32(100)), Protocol: domain.ProtocolHTTP,
	})

	if err := svc.FailRun(context.Background(), "run-1", "final validation at 15 RPS failed: signal: killed"); err != nil {
		t.Fatal(err)
	}
	run, _ := repo.GetRun(context.Background(), "run-1")
	if run.Status != domain.StatusFailed {
		t.Fatalf("status=%s", run.Status)
	}
	if run.FailureReason != "final validation at 15 RPS failed: signal: killed" {
		t.Fatalf("failure_reason=%q", run.FailureReason)
	}
	if len(orch.cleaned) != 1 {
		t.Fatalf("cleaned=%d", len(orch.cleaned))
	}
}

func TestStartRejectsInvalidProtocol(t *testing.T) {
	repo := memory.NewRunRepo()
	orch := &orchStub{}
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	svc := app.NewService(slog.Default(), repo, orch, func() string { return "run-1" }, func() time.Time { return now }, time.Minute)
	if _, err := svc.StartTest(context.Background(), domain.StartTestCmd{
		Tenant: "acme", Name: "resnet", S3Path: "s3://m/resnet", TargetRPS: new(int32(50)), Duration: ptr(time.Minute), Protocol: "tcp",
	}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestStartRejectsMixedModes(t *testing.T) {
	repo := memory.NewRunRepo()
	orch := &orchStub{}
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	svc := app.NewService(slog.Default(), repo, orch, func() string { return "run-1" }, func() time.Time { return now }, time.Minute)
	if _, err := svc.StartTest(context.Background(), domain.StartTestCmd{
		Tenant: "acme", Name: "resnet", S3Path: "s3://m/resnet", TargetRPS: new(int32(50)), Duration: ptr(time.Minute), MaxLatencyMS: new(int32(100)), Protocol: domain.ProtocolHTTP,
	}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestStartRejectsMissingModes(t *testing.T) {
	repo := memory.NewRunRepo()
	orch := &orchStub{}
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	svc := app.NewService(slog.Default(), repo, orch, func() string { return "run-1" }, func() time.Time { return now }, time.Minute)
	if _, err := svc.StartTest(context.Background(), domain.StartTestCmd{
		Tenant: "acme", Name: "resnet", S3Path: "s3://m/resnet", Protocol: domain.ProtocolHTTP,
	}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestCancelInactiveRunReturnsErrCancelInactive(t *testing.T) {
	repo := memory.NewRunRepo()
	orch := &orchStub{}
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	svc := app.NewService(slog.Default(), repo, orch, func() string { return "run-1" }, func() time.Time { return now }, time.Minute)
	_, _ = svc.StartTest(context.Background(), domain.StartTestCmd{
		Tenant: "acme", Name: "resnet", S3Path: "s3://m/resnet", MaxLatencyMS: new(int32(100)), Protocol: domain.ProtocolHTTP,
	})
	_ = svc.FailRun(context.Background(), "run-1", "done")

	err := svc.CancelRun(context.Background(), "run-1")
	if !errors.Is(err, app.ErrCancelInactive) {
		t.Fatalf("ожидалась app.ErrCancelInactive, получил: %v", err)
	}
}
