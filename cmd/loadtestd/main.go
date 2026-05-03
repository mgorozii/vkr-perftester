package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/mgorozii/perftester/internal/adapters/httpapi"
	"github.com/mgorozii/perftester/internal/adapters/k8s"
	"github.com/mgorozii/perftester/internal/adapters/memory"
	"github.com/mgorozii/perftester/internal/adapters/postgres"
	"github.com/mgorozii/perftester/internal/app"
	"github.com/mgorozii/perftester/internal/config"
	"github.com/mgorozii/perftester/internal/telemetry"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := runMain(logger); err != nil {
		logger.Error("loadtestd failed", "error", err)
		os.Exit(1)
	}
}

func runMain(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	tel, err := telemetry.Init(context.Background(), "loadtestd", true, cfg.SamplingRatio)
	if err != nil {
		return err
	}
	defer func() {
		if err := tel.Shutdown(context.Background()); err != nil {
			logger.Error("failed to shutdown telemetry", "error", err)
		}
	}()
	logger = tel.Logger

	repo := app.RunRepository(memory.NewRunRepo())
	if cfg.DatabaseURL != "" {
		pg, err := openPostgres(logger, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer func() {
			if err := pg.Close(); err != nil {
				logger.Error("failed to close postgres", "error", err)
			}
		}()
		repo = pg
	}
	api, err := k8s.NewAPI(k8s.Options{
		ControllerNamespace: cfg.ControllerNS,
		StorageSecretName:   cfg.StorageSecret,
		K6Image:             cfg.K6Image,
		WebhookURL:          cfg.WebhookURL,
		HTTPURL:             cfg.ModelHTTP,
		GRPCURL:             cfg.ModelGRPC,
	})
	if err != nil {
		return err
	}
	otelEnv := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT":         cfg.OTLPEndpoint,
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":  cfg.OTLPTracesURL,
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": cfg.OTLPMetricsURL,
		"OTEL_SAMPLING_RATIO":                 fmt.Sprintf("%f", cfg.SamplingRatio),
	}
	orch := k8s.New(logger, api, cfg.WebhookURL, cfg.ModelHTTP, cfg.ModelGRPC, otelEnv)
	svc := app.NewService(logger, repo, orch, newID, time.Now, cfg.RunTimeout)
	go sweep(logger, svc, cfg.SweepEvery)
	go serveMetrics(logger, cfg.MetricsAddr, tel.MetricsHandler)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(logger, svc, nil).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
	logger.Info("starting http server", "addr", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil {
		return err
	}
	return nil
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UTC().Format("20060102150405")
	}
	return hex.EncodeToString(b[:])
}

func sweep(logger *slog.Logger, svc *app.Service, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for range ticker.C {
		if err := svc.TimeoutSweep(context.Background()); err != nil {
			logger.Error("timeout sweep failed", "error", err)
		}
	}
}

func serveMetrics(logger *slog.Logger, addr string, handler http.Handler) {
	if addr == "" || handler == nil {
		return
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	logger.Info("starting metrics server", "addr", addr)
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("metrics server failed", "error", err)
	}
}

func openPostgres(logger *slog.Logger, dsn string) (*postgres.Repo, error) {
	var last error
	for range 60 {
		pg, err := postgres.Open(context.Background(), logger, dsn)
		if err == nil {
			return pg, nil
		}
		last = err
		logger.Warn("waiting for postgres", "error", err)
		time.Sleep(time.Second)
	}
	return nil, last
}
