package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/mgorozii/perftester/internal/domain"
)

type ResourceNames struct {
	Namespace        string
	ServingRuntime   string
	InferenceService string
	Job              string
}

type Client interface {
	EnsureInference(context.Context, domain.Run, ResourceNames) error
	CreateLoadJob(context.Context, string, string, string, map[string]string) error
	DeleteNamespace(context.Context, string) error
}

type Orchestrator struct {
	logger     *slog.Logger
	k8s        Client
	httpURL    string
	grpcURL    string
	webhookURL string
}

func New(logger *slog.Logger, k8s Client, webhook, http, grpc string) *Orchestrator {
	return &Orchestrator{
		logger:     logger,
		k8s:        k8s,
		httpURL:    http,
		grpcURL:    grpc,
		webhookURL: webhook,
	}
}

func Names(run domain.Run) ResourceNames {
	base := run.Tenant + "-" + run.ModelName + "-" + run.ID
	return ResourceNames{
		Namespace:        base,
		ServingRuntime:   run.ModelName + "-runtime",
		InferenceService: run.ModelName,
		Job:              run.ID + "-k6",
	}
}

func (o *Orchestrator) Submit(ctx context.Context, run domain.Run) error {
	n := Names(run)
	o.logger.Info("submitting run", "run_id", run.ID, "namespace", n.Namespace)

	if err := o.k8s.EnsureInference(ctx, run, n); err != nil {
		o.logger.Error("failed to ensure inference environment", "run_id", run.ID, "namespace", n.Namespace, "error", err)
		return err
	}
	o.logger.Info("inference environment ready", "run_id", run.ID)

	rps := 0
	if run.TargetRPS != nil {
		rps = int(*run.TargetRPS)
	}
	dur := ""
	if run.Duration != nil {
		dur = run.Duration.String()
	}

	config := map[string]any{
		"run_id":         run.ID,
		"model_name":     run.ModelName,
		"target_rps":     rps,
		"duration":       dur,
		"protocol":       string(run.Protocol),
		"http_url":       o.httpURL,
		"grpc_url":       o.grpcURL,
		"webhook_url":    o.webhookURL,
		"payload":        run.Payload,
		"enable_metrics": true,
	}
	configJS, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshal job config: %w", err)
	}

	env := map[string]string{
		"STEPS_URL":  strings.Replace(o.webhookURL, "/report", "/search_steps", 1),
		"STATUS_URL": strings.Replace(o.webhookURL, "/report", "/run_status", 1),
	}
	if run.MaxLatencyMS != nil {
		env["MAX_LATENCY_MS"] = strconv.Itoa(int(*run.MaxLatencyMS))
	}

	if err := o.k8s.CreateLoadJob(ctx, n.Namespace, n.Job, string(configJS), env); err != nil {
		o.logger.Error("failed to create load job", "job", n.Job, "error", err)
		return err
	}
	o.logger.Info("load job created", "job", n.Job, "run_id", run.ID)
	return nil
}

func (o *Orchestrator) Cleanup(ctx context.Context, run domain.Run) error {
	n := Names(run)
	o.logger.Info("cleaning up run resources", "run_id", run.ID, "namespace", n.Namespace)

	if err := o.k8s.DeleteNamespace(ctx, n.Namespace); err != nil {
		o.logger.Error("failed to delete namespace", "run_id", run.ID, "namespace", n.Namespace, "error", err)
		return err
	}

	o.logger.Info("cleanup completed", "run_id", run.ID, "namespace", n.Namespace)
	return nil
}
