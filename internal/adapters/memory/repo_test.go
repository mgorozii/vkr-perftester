package memory_test

import (
	"context"
	"testing"

	"github.com/mgorozii/perftester/internal/adapters/memory"
	"github.com/mgorozii/perftester/internal/domain"
)

func TestSaveSearchStepsUpsertsByStepNumber(t *testing.T) {
	repo := memory.NewRunRepo()
	ctx := context.Background()
	if err := repo.SaveSearchSteps(ctx, []domain.SearchStep{{RunID: "run-1", StepNumber: 1, RPS: 10}, {RunID: "run-1", StepNumber: 2, RPS: 20}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveSearchSteps(ctx, []domain.SearchStep{{RunID: "run-1", StepNumber: 2, RPS: 25, StopReason: "latency"}, {RunID: "run-1", StepNumber: 3, RPS: 30}}); err != nil {
		t.Fatal(err)
	}
	steps, err := repo.ListSearchSteps(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 {
		t.Fatalf("steps=%d", len(steps))
	}
	if steps[1].RPS != 25 {
		t.Fatalf("rps=%d", steps[1].RPS)
	}
	if steps[1].StopReason != "latency" {
		t.Fatalf("stop_reason=%q", steps[1].StopReason)
	}
}
