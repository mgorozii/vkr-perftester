package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"strconv"
	"strings"

	"github.com/mgorozii/perftester/internal/domain"
	"github.com/mgorozii/perftester/internal/telemetry"
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
	otelEnv    map[string]string
}

func New(logger *slog.Logger, k8s Client, webhook, http, grpc string, otelEnv map[string]string) *Orchestrator {
	return &Orchestrator{
		logger:     logger.With("component", "k8s.orchestrator"),
		k8s:        k8s,
		httpURL:    http,
		grpcURL:    grpc,
		webhookURL: webhook,
		otelEnv:    otelEnv,
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
	log := o.logger.With("run_id", run.ID, "namespace", n.Namespace)
	log.InfoContext(ctx, "submitting run")

	if err := o.k8s.EnsureInference(ctx, run, n); err != nil {
		log.ErrorContext(ctx, "failed to ensure inference environment", "error", err)
		return err
	}
	log.InfoContext(ctx, "inference environment ready")

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
	maps.Copy(env, telemetry.InjectEnv(ctx))
	for k, v := range o.otelEnv {
		if v != "" {
			env[k] = v
		}
	}
	if run.MaxLatencyMS != nil {
		env["MAX_LATENCY_MS"] = strconv.Itoa(int(*run.MaxLatencyMS))
	}

	if err := o.k8s.CreateLoadJob(ctx, n.Namespace, n.Job, string(configJS), env); err != nil {
		log.With("job", n.Job).ErrorContext(ctx, "failed to create load job", "error", err)
		return err
	}
	log.With("job", n.Job).InfoContext(ctx, "load job created")
	return nil
}

func (o *Orchestrator) Cleanup(ctx context.Context, run domain.Run) error {
	n := Names(run)
	log := o.logger.With("run_id", run.ID, "namespace", n.Namespace)
	log.InfoContext(ctx, "cleaning up run resources")

	if err := o.k8s.DeleteNamespace(ctx, n.Namespace); err != nil {
		log.ErrorContext(ctx, "failed to delete namespace", "error", err)
		return err
	}

	log.InfoContext(ctx, "cleanup completed")
	return nil
}
