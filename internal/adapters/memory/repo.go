package memory

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/mgorozii/perftester/internal/app"
	"github.com/mgorozii/perftester/internal/domain"
)

type RunRepo struct {
	mu      sync.RWMutex
	runs    map[string]domain.Run
	metrics []domain.Metric
	steps   map[string][]domain.SearchStep
}

func NewRunRepo() *RunRepo {
	return &RunRepo{runs: map[string]domain.Run{}, steps: map[string][]domain.SearchStep{}}
}

func (r *RunRepo) CreateRun(_ context.Context, run domain.Run) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[run.ID] = run
	return nil
}

func (r *RunRepo) UpdateRunStatus(_ context.Context, id string, status domain.Status, updatedAt time.Time, failureReason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[id]
	if !ok {
		return app.ErrNotFound
	}
	run.Status = status
	run.FailureReason = failureReason
	run.UpdatedAt = updatedAt
	r.runs[id] = run
	return nil
}

func (r *RunRepo) GetRun(_ context.Context, id string) (domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	run, ok := r.runs[id]
	if !ok {
		return domain.Run{}, app.ErrNotFound
	}
	return run, nil
}

func (r *RunRepo) ListRuns(_ context.Context, filter app.RunFilter) ([]domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.Run, 0, len(r.runs))
	for _, run := range r.runs {
		if filter.RunID != "" && run.ID != filter.RunID {
			continue
		}
		if filter.Tenant != "" && run.Tenant != filter.Tenant {
			continue
		}
		if filter.ModelName != "" && run.ModelName != filter.ModelName {
			continue
		}
		out = append(out, run)
	}
	slices.SortFunc(out, func(a, b domain.Run) int {
		if c := cmp.Compare(b.CreatedAt.UnixNano(), a.CreatedAt.UnixNano()); c != 0 {
			return c
		}
		return cmp.Compare(b.ID, a.ID)
	})
	return out, nil
}

func (r *RunRepo) DeleteRun(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.runs[id]; !ok {
		return app.ErrNotFound
	}
	delete(r.runs, id)
	delete(r.steps, id)
	out := r.metrics[:0]
	for _, metric := range r.metrics {
		if metric.RunID != id {
			out = append(out, metric)
		}
	}
	r.metrics = out
	return nil
}

func (r *RunRepo) SaveMetrics(_ context.Context, metrics []domain.Metric) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.metrics = append(r.metrics, metrics...)
	return nil
}

func (r *RunRepo) SaveSearchSteps(_ context.Context, steps []domain.SearchStep) error {
	if len(steps) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id := steps[0].RunID
	current := r.steps[id]
	idx := make(map[int]int, len(current))
	for i, step := range current {
		idx[step.StepNumber] = i
	}
	for _, step := range steps {
		if i, ok := idx[step.StepNumber]; ok {
			current[i] = step
			continue
		}
		idx[step.StepNumber] = len(current)
		current = append(current, step)
	}
	r.steps[id] = current
	return nil
}

func (r *RunRepo) ListSearchSteps(_ context.Context, runID string) ([]domain.SearchStep, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	steps := r.steps[runID]
	out := make([]domain.SearchStep, len(steps))
	copy(out, steps)
	return out, nil
}

func (r *RunRepo) ListMetrics(ctx context.Context, filter app.MetricsFilter) ([]domain.Metric, error) {
	runs, err := r.ListRuns(ctx, app.RunFilter{Tenant: filter.Tenant, ModelName: filter.ModelName, RunID: filter.RunID})
	if err != nil {
		return nil, err
	}
	allowed := map[string]struct{}{}
	for _, run := range runs {
		allowed[run.ID] = struct{}{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.Metric, 0, len(r.metrics))
	for _, metric := range r.metrics {
		if len(allowed) > 0 {
			if _, ok := allowed[metric.RunID]; !ok {
				continue
			}
		}
		if filter.MetricName != "" && metric.MetricName != filter.MetricName {
			continue
		}
		if !filter.From.IsZero() && metric.Timestamp.Before(filter.From) {
			continue
		}
		if !filter.To.IsZero() && metric.Timestamp.After(filter.To) {
			continue
		}
		out = append(out, metric)
	}
	return out, nil
}

func (r *RunRepo) ListActiveRuns(_ context.Context) ([]domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []domain.Run
	for _, run := range r.runs {
		if run.Status == domain.StatusPending || run.Status == domain.StatusRunning {
			out = append(out, run)
		}
	}
	return out, nil
}
