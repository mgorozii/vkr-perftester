package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	repo := app.RunRepository(memory.NewRunRepo())
	if cfg.DatabaseURL != "" {
		pg, err := openPostgres(logger, cfg.DatabaseURL)
		if err != nil {
			logger.Error("failed to open postgres", "error", err)
			os.Exit(1)
		}
		defer func() { _ = pg.Close() }()
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
		logger.Error("failed to create k8s api", "error", err)
		return
	}
	orch := k8s.New(logger, api, cfg.WebhookURL, cfg.ModelHTTP, cfg.ModelGRPC)
	svc := app.NewService(logger, repo, orch, newID, time.Now, cfg.RunTimeout)
	go sweep(svc, cfg.SweepEvery)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(logger, svc).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
	logger.Info("starting http server", "addr", cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("http server failed", "error", err)
	}
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UTC().Format("20060102150405")
	}
	return hex.EncodeToString(b[:])
}

func sweep(svc *app.Service, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for range ticker.C {
		_ = svc.TimeoutSweep(context.Background())
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
