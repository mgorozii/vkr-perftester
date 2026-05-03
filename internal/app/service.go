package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mgorozii/perftester/internal/domain"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrCancelInactive = errors.New("only active runs can be cancelled")
)

type RunRepository interface {
	CreateRun(context.Context, domain.Run) error
	UpdateRunStatus(context.Context, string, domain.Status, time.Time, string) error
	GetRun(context.Context, string) (domain.Run, error)
	ListRuns(context.Context, RunFilter) ([]domain.Run, error)
	DeleteRun(context.Context, string) error
	SaveMetrics(context.Context, []domain.Metric) error
	SaveSearchSteps(context.Context, []domain.SearchStep) error
	ListSearchSteps(context.Context, string) ([]domain.SearchStep, error)
	ListMetrics(context.Context, MetricsFilter) ([]domain.Metric, error)
	ListActiveRuns(context.Context) ([]domain.Run, error)
}

type Orchestrator interface {
	Submit(context.Context, domain.Run) error
	Cleanup(context.Context, domain.Run) error
}

type (
	IDGen func() string
	Now   func() time.Time
)

type RunFilter struct {
	Tenant    string
	ModelName string
	RunID     string
}

type MetricsFilter struct {
	Tenant     string
	ModelName  string
	RunID      string
	MetricName string
	From       time.Time
	To         time.Time
}

type Service struct {
	logger     *slog.Logger
	repo       RunRepository
	orch       Orchestrator
	idgen      IDGen
	now        Now
	runTimeout time.Duration
}

func NewService(logger *slog.Logger, repo RunRepository, orch Orchestrator, idgen IDGen, now Now, runTimeout time.Duration) *Service {
	return &Service{logger: logger.With("component", "app.service"), repo: repo, orch: orch, idgen: idgen, now: now, runTimeout: runTimeout}
}

func (s *Service) StartTest(ctx context.Context, cmd domain.StartTestCmd) (string, error) {
	if err := validateStart(cmd); err != nil {
		return "", err
	}
	now := s.now()
	run := domain.Run{
		ID:           s.idgen(),
		Tenant:       cmd.Tenant,
		ModelName:    cmd.Name,
		S3Path:       cmd.S3Path,
		ModelFormat:  cmd.ModelFormat,
		TargetRPS:    cmd.TargetRPS,
		Duration:     cmd.Duration,
		MaxLatencyMS: cmd.MaxLatencyMS,
		Payload:      cmd.Payload,
		Protocol:     cmd.Protocol,
		Status:       domain.StatusPending,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	log := s.logger.With("run_id", run.ID, "tenant", run.Tenant, "model", run.ModelName)
	log.InfoContext(ctx, "starting test")
	if err := s.repo.CreateRun(ctx, run); err != nil {
		return "", err
	}
	if err := s.orch.Submit(ctx, run); err != nil {
		if failErr := s.FailRun(context.Background(), run.ID, err.Error()); failErr != nil {
			log.ErrorContext(ctx, "failed to mark run as failed", "error", failErr)
		}
		return "", err
	}
	if err := s.repo.UpdateRunStatus(ctx, run.ID, domain.StatusRunning, s.now(), ""); err != nil {
		return "", err
	}
	return run.ID, nil
}

func validateStart(cmd domain.StartTestCmd) error {
	if cmd.Tenant == "" || cmd.Name == "" || cmd.S3Path == "" {
		return errors.New("tenant, name, s3_path required")
	}
	search := cmd.MaxLatencyMS != nil
	fixed := cmd.TargetRPS != nil || cmd.Duration != nil
	if search == fixed {
		return errors.New("provide either max_latency_ms or target_rps with duration")
	}
	if search {
		if *cmd.MaxLatencyMS <= 0 {
			return errors.New("max_latency_ms must be > 0")
		}
	} else {
		if cmd.TargetRPS == nil || *cmd.TargetRPS <= 0 {
			return errors.New("target_rps must be > 0")
		}
		if cmd.Duration == nil || *cmd.Duration <= 0 {
			return errors.New("duration must be > 0")
		}
	}
	switch cmd.Protocol {
	case domain.ProtocolHTTP, domain.ProtocolGRPC:
		return nil
	default:
		return errors.New("protocol must be HTTP or gRPC")
	}
}

func (s *Service) AcceptReport(ctx context.Context, report domain.Report) error {
	run, err := s.repo.GetRun(ctx, report.RunID)
	if err != nil {
		return err
	}
	metrics := make([]domain.Metric, 0, len(report.Metrics))
	for _, point := range report.Metrics {
		ts := point.Timestamp
		if ts.IsZero() {
			ts = report.Received
		}
		metrics = append(metrics, domain.Metric{
			RunID:      run.ID,
			Timestamp:  ts,
			MetricName: point.MetricName,
			Value:      point.Value,
		})
	}
	if err := s.repo.SaveMetrics(ctx, metrics); err != nil {
		return err
	}
	if err := s.repo.UpdateRunStatus(ctx, run.ID, domain.StatusSuccess, s.now(), ""); err != nil {
		return err
	}
	return s.orch.Cleanup(ctx, run)
}

func (s *Service) FailRun(ctx context.Context, runID, reason string) error {
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	log := s.logger.With("run_id", run.ID, "tenant", run.Tenant, "model", run.ModelName)
	log.ErrorContext(ctx, "run failed", "reason", reason)
	if err := s.orch.Cleanup(ctx, run); err != nil {
		log.ErrorContext(ctx, "cleanup failed for failed run", "error", err)
	}
	return s.repo.UpdateRunStatus(ctx, run.ID, domain.StatusFailed, s.now(), reason)
}

func (s *Service) AcceptSearchSteps(ctx context.Context, runID string, steps []domain.SearchStep) error {
	for i := range steps {
		steps[i].RunID = runID
	}
	return s.repo.SaveSearchSteps(ctx, steps)
}

func (s *Service) ListSearchSteps(ctx context.Context, runID string) ([]domain.SearchStep, error) {
	return s.repo.ListSearchSteps(ctx, runID)
}

func (s *Service) TimeoutSweep(ctx context.Context) error {
	runs, err := s.repo.ListActiveRuns(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	for _, run := range runs {
		log := s.logger.With("run_id", run.ID, "tenant", run.Tenant, "model", run.ModelName)
		timeout := s.runTimeout
		baseBuffer := 10 * time.Minute

		if run.Duration != nil {
			fixedTimeout := *run.Duration + baseBuffer
			if fixedTimeout < timeout {
				timeout = fixedTimeout
			}
		}

		if now.After(run.CreatedAt.Add(timeout)) {
			log.InfoContext(ctx, "timing out stuck run", "duration", time.Since(run.CreatedAt))
			if err := s.orch.Cleanup(ctx, run); err != nil {
				log.ErrorContext(ctx, "cleanup failed for timeout run", "error", err)
				return err
			}
			if err := s.repo.UpdateRunStatus(ctx, run.ID, domain.StatusFailed, now, "run timed out"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) ListRuns(ctx context.Context, filter RunFilter) ([]domain.Run, error) {
	return s.repo.ListRuns(ctx, filter)
}

func (s *Service) GetRun(ctx context.Context, id string) (domain.Run, error) {
	return s.repo.GetRun(ctx, id)
}

func (s *Service) DeleteRun(ctx context.Context, id string) error {
	return s.repo.DeleteRun(ctx, id)
}

func (s *Service) CancelRun(ctx context.Context, id string) error {
	run, err := s.repo.GetRun(ctx, id)
	if err != nil {
		return err
	}
	if run.Status != domain.StatusPending && run.Status != domain.StatusRunning {
		return ErrCancelInactive
	}
	log := s.logger.With("run_id", run.ID, "tenant", run.Tenant, "model", run.ModelName)
	log.InfoContext(ctx, "canceling run by operator")
	if err := s.orch.Cleanup(ctx, run); err != nil {
		log.ErrorContext(ctx, "cleanup failed during cancellation", "error", err)
	}
	return s.repo.UpdateRunStatus(ctx, run.ID, domain.StatusFailed, s.now(), "cancelled by operator")
}

func (s *Service) QueryMetrics(ctx context.Context, filter MetricsFilter) ([]domain.Metric, error) {
	return s.repo.ListMetrics(ctx, filter)
}
