package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/mgorozii/perftester/internal/config"
	"github.com/mgorozii/perftester/internal/k6"
	"github.com/mgorozii/perftester/internal/telemetry"
)

const (
	searchWarmupRPS      = 1
	initialSearchRPS     = 2
	warmupDuration       = 15 * time.Second
	stepDuration         = 15 * time.Second
	defaultFinalRun      = 15 * time.Second
	maxBinarySteps       = 20
	maxSanityRPS         = 1e6
	maxErrorRate         = 0.01
	stopReasonLatency    = "latency"
	stopReasonErrors     = "error_rate"
	stopReasonThroughput = "throughput"
	stopReasonExecError  = "executor_error"
)

type Runner struct {
	config.Executor
	BaseConfig k6.Config
	WorkDir    string
	Root       *os.Root
}

type K6Summary struct {
	Metrics map[string]map[string]float64 `json:"metrics"`
}

type SearchStep struct {
	StepNumber      int     `json:"step_number"`
	RPS             int     `json:"rps"`
	ActualRPS       float64 `json:"actual_rps"`
	VUs             float64 `json:"vus"`
	MaxVUs          float64 `json:"max_vus"`
	DurationSeconds int     `json:"duration_seconds"`
	P99LatencyMS    float64 `json:"p99_latency_ms"`
	ErrorRate       float64 `json:"error_rate"`
	StopReason      string  `json:"stop_reason,omitempty"`
	Error           string  `json:"error,omitempty"`
}

type stepResult struct {
	rps        int
	actualRPS  float64
	vus        float64
	maxVUs     float64
	p99        float64
	errorRate  float64
	stopReason string
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := runMain(logger); err != nil {
		logger.Error("executor failed", "error", err)
		os.Exit(1)
	}
}

func runMain(logger *slog.Logger) error {
	cfg, err := config.LoadExecutor()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	tel, err := telemetry.Init(context.Background(), "executor", false, cfg.SamplingRatio)
	if err != nil {
		return err
	}
	defer func() {
		if err := tel.Shutdown(context.Background()); err != nil {
			logger.Error("failed to shutdown telemetry", "error", err)
		}
	}()
	logger = tel.Logger

	return run(telemetry.ExtractEnv(context.Background()), cfg, logger, telemetry.NewHTTPClient())
}

func run(ctx context.Context, cfg config.Executor, logger *slog.Logger, client *http.Client) error {
	data, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read k6 config: %w", err)
	}

	var base k6.Config
	if err := json.Unmarshal(data, &base); err != nil {
		return fmt.Errorf("failed to unmarshal k6 config: %w", err)
	}

	workDir, err := os.MkdirTemp("", "perftester-*")
	if err != nil {
		return fmt.Errorf("failed to create work dir: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(workDir); err != nil {
			logger.Error("failed to remove work dir", "path", workDir, "error", err)
		}
	}()

	root, err := os.OpenRoot(workDir)
	if err != nil {
		return fmt.Errorf("failed to open root: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			logger.Error("failed to close root", "path", workDir, "error", err)
		}
	}()

	r := &Runner{
		Executor:   cfg,
		BaseConfig: base,
		WorkDir:    workDir,
		Root:       root,
	}
	log := logger.With("run_id", r.BaseConfig.RunID)

	if err := root.WriteFile("script.js", []byte(k6.JS), 0o600); err != nil {
		return fmt.Errorf("failed to write script: %w", err)
	}

	if r.SLO > 0 {
		log.InfoContext(ctx, "search mode", "slo_ms", r.SLO)
		err = runSearch(ctx, log, client, r)
	} else {
		log.InfoContext(ctx, "fixed mode", "rps", r.BaseConfig.TargetRPS, "duration", r.BaseConfig.Duration)
		err = runFixed(ctx, log, client, r)
	}
	if err != nil {
		reportFailure(ctx, client, log, r, err)
	}
	return err
}

func runFixed(ctx context.Context, logger *slog.Logger, _ *http.Client, r *Runner) error {
	if err := waitForModel(ctx, logger, r); err != nil {
		return err
	}
	dur, err := time.ParseDuration(r.BaseConfig.Duration)
	if err != nil {
		return fmt.Errorf("invalid fixed duration %q: %w", r.BaseConfig.Duration, err)
	}
	_, err = runK6(ctx, logger, r, r.BaseConfig.TargetRPS, dur, true)
	return err
}

func runSearch(ctx context.Context, logger *slog.Logger, client *http.Client, r *Runner) error {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	log := logger.With("run_id", r.BaseConfig.RunID)

	if err := waitForModel(ctx, log, r); err != nil {
		return err
	}
	if _, err := runK6(ctx, log, r, searchWarmupRPS, warmupDuration, false); err != nil {
		return fmt.Errorf("warm-up failed: %w", err)
	}

	stepNumber := 1
	lastSuccess := searchWarmupRPS
	firstFailure := 0

	for rps := initialSearchRPS; ; rps *= 2 {
		res, err := probe(ctx, log, r, rps)
		if err != nil {
			saveStep(ctx, client, log, r, failedStep(stepNumber, rps, stepDuration, err))
			return fmt.Errorf("search probe step %d at %d RPS failed: %w", stepNumber, rps, err)
		}
		saveStep(ctx, client, log, r, toSearchStep(stepNumber, res, stepDuration))
		stepNumber++
		if res.stopReason != "" {
			firstFailure = rps
			break
		}
		lastSuccess = rps
		if rps >= maxSanityRPS {
			break
		}
	}

	best := lastSuccess
	if firstFailure > 0 {
		low, high := lastSuccess, firstFailure
		for i := 0; i < maxBinarySteps && high-low > 1; i++ {
			mid := low + (high-low)/2
			res, err := probe(ctx, log, r, mid)
			if err != nil {
				saveStep(ctx, client, log, r, failedStep(stepNumber, mid, stepDuration, err))
				return fmt.Errorf("binary search step %d at %d RPS failed: %w", stepNumber, mid, err)
			}
			saveStep(ctx, client, log, r, toSearchStep(stepNumber, res, stepDuration))
			stepNumber++
			if res.stopReason != "" {
				high = mid
				continue
			}
			low = mid
			best = mid
		}
	}

	if err := waitForModel(ctx, log, r); err != nil {
		return err
	}
	duration := finalDuration(r)
	log.InfoContext(ctx, "starting final validation", "rps", best, "duration", duration)
	summary, err := runK6(ctx, log, r, best, duration, true)
	if err != nil {
		saveStep(ctx, client, log, r, failedStep(stepNumber, best, duration, fmt.Errorf("final validation failed: %w", err)))
		return fmt.Errorf("final validation at %d RPS failed: %w", best, err)
	}

	res := summarize(summary, best)
	saveStep(ctx, client, log, r, toSearchStep(stepNumber, res, duration))

	return nil
}

func probe(ctx context.Context, logger *slog.Logger, r *Runner, rps int) (stepResult, error) {
	summary, err := runK6(ctx, logger, r, rps, stepDuration, false)
	if err != nil {
		return stepResult{}, fmt.Errorf("probe %d RPS failed: %w", rps, err)
	}
	res := summarize(summary, rps)
	res.stopReason = searchStopReason(r, res)
	logger.InfoContext(ctx, "probe result", "rps", rps, "actual", res.actualRPS, "p99_ms", res.p99, "error_rate", res.errorRate, "stop", res.stopReason)
	return res, nil
}

func summarize(summary K6Summary, rps int) stepResult {
	p99 := getMetric(summary, "http_req_duration", "p(99)")
	errRate := getMetric(summary, "http_req_failed", "rate")
	if errRate == 0 {
		errRate = getMetric(summary, "http_req_failed", "value")
	}
	actualRPS := getMetric(summary, "http_reqs", "rate")
	vus := getMetric(summary, "vus", "max")
	maxVUs := getMetric(summary, "vus_max", "value")
	return stepResult{rps: rps, actualRPS: actualRPS, vus: vus, maxVUs: maxVUs, p99: p99, errorRate: errRate}
}

func searchStopReason(r *Runner, res stepResult) string {
	switch {
	case res.p99 > float64(r.SLO):
		return stopReasonLatency
	case res.errorRate > maxErrorRate:
		return stopReasonErrors
	case res.rps > 30 && res.actualRPS < float64(res.rps)*0.7:
		return stopReasonThroughput
	default:
		return ""
	}
}

func waitForModel(ctx context.Context, logger *slog.Logger, r *Runner) error {
	for range 30 {
		if err := ctx.Err(); err != nil {
			return err
		}
		readyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		_, err := runK6(readyCtx, logger, r, 1, time.Second, false)
		cancel()
		if err == nil {
			return nil
		}
		logger.DebugContext(ctx, "model not ready, waiting...", "error", err)
		time.Sleep(5 * time.Second)
	}
	return errors.New("model readiness timed out")
}

func getMetric(summary K6Summary, name, key string) float64 {
	m, ok := summary.Metrics[name]
	if !ok {
		return 0
	}
	return m[key]
}

func runK6(ctx context.Context, logger *slog.Logger, r *Runner, rps int, duration time.Duration, enableMetrics bool, extraArgs ...string) (K6Summary, error) {
	runConfigName := fmt.Sprintf("config-%d-%d.json", rps, time.Now().UnixNano())
	summaryName := fmt.Sprintf("summary-%d-%d.json", rps, time.Now().UnixNano())

	runConfig := r.BaseConfig
	runConfig.TargetRPS = rps
	runConfig.Duration = duration.String()
	runConfig.EnableMetrics = enableMetrics
	runConfig.PreAllocatedVUs = max(rps, 10)
	runConfig.MaxVUs = 5000

	js, err := json.Marshal(runConfig)
	if err != nil {
		return K6Summary{}, fmt.Errorf("failed to marshal k6 config: %w", err)
	}
	if err := r.Root.WriteFile(runConfigName, js, 0o600); err != nil {
		return K6Summary{}, fmt.Errorf("failed to write k6 config: %w", err)
	}

	args := make([]string, 0, 6+len(extraArgs))
	args = append(args, "run", "script.js", "--summary-export", summaryName, "--summary-trend-stats", "avg,min,med,max,p(90),p(95),p(99)")
	args = append(args, extraArgs...)

	cmd := exec.CommandContext(ctx, "k6", args...) //nolint:gosec
	cmd.Dir = r.WorkDir
	cmd.Env = append(os.Environ(), "CONFIG_PATH="+runConfigName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	logger.DebugContext(ctx, "running k6", "rps", rps, "duration", duration)
	if err := cmd.Run(); err != nil {
		return K6Summary{}, err
	}

	data, err := r.Root.ReadFile(summaryName)
	if err != nil {
		return K6Summary{}, err
	}
	return parseSummary(data)
}

func parseSummary(data []byte) (K6Summary, error) {
	var s K6Summary
	if err := json.Unmarshal(data, &s); err != nil {
		return K6Summary{}, fmt.Errorf("invalid k6 summary: %w", err)
	}
	return s, nil
}

func toSearchStep(stepNumber int, res stepResult, duration time.Duration) SearchStep {
	return SearchStep{
		StepNumber:      stepNumber,
		RPS:             res.rps,
		ActualRPS:       res.actualRPS,
		VUs:             res.vus,
		MaxVUs:          res.maxVUs,
		DurationSeconds: int(duration.Seconds()),
		P99LatencyMS:    res.p99,
		ErrorRate:       res.errorRate,
		StopReason:      res.stopReason,
	}
}

func failedStep(stepNumber, rps int, duration time.Duration, err error) SearchStep {
	return SearchStep{
		StepNumber:      stepNumber,
		RPS:             rps,
		DurationSeconds: int(duration.Seconds()),
		StopReason:      stopReasonExecError,
		Error:           err.Error(),
	}
}

var retryDelay = time.Second

func postJSON(ctx context.Context, client *http.Client, logger *slog.Logger, url string, body []byte) {
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				logger.ErrorContext(ctx, "webhook aborted", "url", url)
				return
			case <-time.After(retryDelay):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			logger.ErrorContext(ctx, "failed to build webhook request", "url", url, "error", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			logger.ErrorContext(ctx, "webhook post failed", "url", url, "attempt", attempt+1, "error", err)
			continue
		}
		if err := resp.Body.Close(); err != nil {
			logger.ErrorContext(ctx, "failed to close webhook response body", "url", url, "error", err)
		}
		if resp.StatusCode < 300 {
			return
		}
		logger.ErrorContext(ctx, "webhook returned unexpected status", "url", url, "status", resp.StatusCode, "attempt", attempt+1)
		if resp.StatusCode < 500 {
			return
		}
	}
}

func saveStep(ctx context.Context, client *http.Client, logger *slog.Logger, r *Runner, step SearchStep) {
	if r.StepsURL == "" {
		return
	}
	body, err := json.Marshal(map[string]any{"run_id": r.BaseConfig.RunID, "steps": []SearchStep{step}})
	if err != nil {
		logger.ErrorContext(ctx, "failed to marshal step payload", "error", err)
		return
	}
	postJSON(ctx, client, logger, r.StepsURL, body)
}

func reportFailure(parent context.Context, client *http.Client, logger *slog.Logger, r *Runner, err error) {
	if r.StatusURL == "" {
		return
	}
	body, marshalErr := json.Marshal(map[string]string{
		"run_id": r.BaseConfig.RunID,
		"status": "FAILED",
		"error":  err.Error(),
	})
	if marshalErr != nil {
		logger.ErrorContext(parent, "failed to marshal failure payload", "error", marshalErr)
		return
	}
	ctx := context.Background()
	if sc := trace.SpanContextFromContext(parent); sc.IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, sc)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	postJSON(ctx, client, logger, r.StatusURL, body)
}

func finalDuration(r *Runner) time.Duration {
	if d, err := time.ParseDuration(r.BaseConfig.Duration); err == nil && d > 0 {
		return d
	}
	return defaultFinalRun
}
