package postgres

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/XSAM/otelsql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"

	"github.com/mgorozii/perftester/internal/app"
	"github.com/mgorozii/perftester/internal/domain"
)

type Repo struct {
	logger *slog.Logger
	db     *sql.DB
	reg    otelmetric.Registration
}

func Open(ctx context.Context, logger *slog.Logger, dsn string) (*Repo, error) {
	logger = logger.With("component", "postgres.repo")
	attrs := append(otelsql.AttributesFromDSN(dsn), attribute.String("db.system.name", "postgresql"))
	db, err := otelsql.Open("pgx", dsn, otelsql.WithAttributes(attrs...))
	if err != nil {
		return nil, err
	}
	reg, err := otelsql.RegisterDBStatsMetrics(db, otelsql.WithAttributes(attrs...))
	if err != nil {
		if closeErr := db.Close(); closeErr != nil {
			logger.ErrorContext(ctx, "failed to close db after metrics init error", "error", closeErr)
		}
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		if unregErr := reg.Unregister(); unregErr != nil {
			logger.ErrorContext(ctx, "failed to unregister db metrics after ping error", "error", unregErr)
		}
		if closeErr := db.Close(); closeErr != nil {
			logger.ErrorContext(ctx, "failed to close db after ping error", "error", closeErr)
		}
		return nil, err
	}
	if err := runMigrations(ctx, logger, db); err != nil {
		if unregErr := reg.Unregister(); unregErr != nil {
			logger.ErrorContext(ctx, "failed to unregister db metrics after migration error", "error", unregErr)
		}
		if closeErr := db.Close(); closeErr != nil {
			logger.ErrorContext(ctx, "failed to close db after migration error", "error", closeErr)
		}
		return nil, err
	}
	return &Repo{logger: logger, db: db, reg: reg}, nil
}

func (r *Repo) Close() error {
	if r.reg != nil {
		if err := r.reg.Unregister(); err != nil {
			r.logger.Error("failed to unregister db metrics", "error", err)
		}
	}
	return r.db.Close()
}

func (r *Repo) CreateRun(ctx context.Context, run domain.Run) error {
	var rps sql.NullInt32
	if run.TargetRPS != nil {
		rps = sql.NullInt32{Int32: *run.TargetRPS, Valid: true}
	}
	var duration sql.NullInt64
	if run.Duration != nil {
		duration = sql.NullInt64{Int64: int64(run.Duration.Seconds()), Valid: true}
	}
	var latency sql.NullInt32
	if run.MaxLatencyMS != nil {
		latency = sql.NullInt32{Int32: *run.MaxLatencyMS, Valid: true}
	}

	_, err := r.db.ExecContext(ctx, `
		insert into runs (id, tenant, model_name, s3_path, model_format, target_rps, duration_seconds, max_latency_ms, payload, protocol, status, failure_reason, created_at, updated_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	`, run.ID, run.Tenant, run.ModelName, run.S3Path, run.ModelFormat, rps, duration, latency, run.Payload, run.Protocol, run.Status, run.FailureReason, run.CreatedAt, run.UpdatedAt)
	return err
}

func (r *Repo) UpdateRunStatus(ctx context.Context, id string, status domain.Status, updatedAt time.Time, failureReason string) error {
	res, err := r.db.ExecContext(ctx, `update runs set status = $2, updated_at = $3, failure_reason = $4 where id = $1`, id, status, updatedAt, failureReason)
	if err != nil {
		return err
	}
	return affectOne(res)
}

func (r *Repo) GetRun(ctx context.Context, id string) (domain.Run, error) {
	row := r.db.QueryRowContext(ctx, `
		select id, tenant, model_name, s3_path, model_format, target_rps, duration_seconds, max_latency_ms, payload, protocol, status, failure_reason, created_at, updated_at
		from runs where id = $1
	`, id)
	return scanRun(row)
}

func (r *Repo) ListRuns(ctx context.Context, filter app.RunFilter) ([]domain.Run, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id, tenant, model_name, s3_path, model_format, target_rps, duration_seconds, max_latency_ms, payload, protocol, status, failure_reason, created_at, updated_at
		from runs
		where ($1 = '' or tenant = $1)
		  and ($2 = '' or model_name = $2)
		  and ($3 = '' or id = $3)
		order by created_at desc
	`, filter.Tenant, filter.ModelName, filter.RunID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			r.logger.ErrorContext(ctx, "failed to close runs rows", "error", err)
		}
	}()
	var out []domain.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (r *Repo) DeleteRun(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `delete from runs where id = $1`, id)
	if err != nil {
		return err
	}
	return affectOne(res)
}

func (r *Repo) SaveMetrics(ctx context.Context, metrics []domain.Metric) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			r.logger.ErrorContext(ctx, "failed to rollback", "error", err)
		}
	}()
	stmt, err := tx.PrepareContext(ctx, `insert into metrics (run_id, timestamp, metric_name, value) values ($1,$2,$3,$4)`)
	if err != nil {
		return err
	}
	defer func() {
		if err := stmt.Close(); err != nil {
			r.logger.ErrorContext(ctx, "failed to close stmt", "error", err)
		}
	}()
	for _, metric := range metrics {
		if _, err := stmt.ExecContext(ctx, metric.RunID, metric.Timestamp, metric.MetricName, metric.Value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repo) SaveSearchSteps(ctx context.Context, steps []domain.SearchStep) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			r.logger.ErrorContext(ctx, "failed to rollback search steps tx", "error", err)
		}
	}()
	stmt, err := tx.PrepareContext(ctx, `
		insert into search_steps (run_id, step_number, rps, actual_rps, vus, max_vus, duration_seconds, p99_latency_ms, error_rate, stop_reason, error)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		on conflict (run_id, step_number) do update set
			rps = excluded.rps,
			actual_rps = excluded.actual_rps,
			vus = excluded.vus,
			max_vus = excluded.max_vus,
			duration_seconds = excluded.duration_seconds,
			p99_latency_ms = excluded.p99_latency_ms,
			error_rate = excluded.error_rate,
			stop_reason = excluded.stop_reason,
			error = excluded.error
	`)
	if err != nil {
		return err
	}
	defer func() {
		if err := stmt.Close(); err != nil {
			r.logger.ErrorContext(ctx, "failed to close search steps stmt", "error", err)
		}
	}()
	for _, step := range steps {
		if _, err := stmt.ExecContext(ctx, step.RunID, step.StepNumber, step.RPS, step.ActualRPS, step.VUs, step.MaxVUs, step.DurationSeconds, step.P99LatencyMS, step.ErrorRate, step.StopReason, step.Error); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repo) ListSearchSteps(ctx context.Context, runID string) ([]domain.SearchStep, error) {
	rows, err := r.db.QueryContext(ctx, `
		select run_id, step_number, rps, actual_rps, vus, max_vus, duration_seconds, p99_latency_ms, error_rate, stop_reason, error
		from search_steps where run_id = $1 order by step_number
	`, runID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			r.logger.ErrorContext(ctx, "failed to close search steps rows", "error", err)
		}
	}()
	var out []domain.SearchStep
	for rows.Next() {
		var step domain.SearchStep
		if err := rows.Scan(&step.RunID, &step.StepNumber, &step.RPS, &step.ActualRPS, &step.VUs, &step.MaxVUs, &step.DurationSeconds, &step.P99LatencyMS, &step.ErrorRate, &step.StopReason, &step.Error); err != nil {
			return nil, err
		}
		out = append(out, step)
	}
	return out, rows.Err()
}

func (r *Repo) ListMetrics(ctx context.Context, filter app.MetricsFilter) ([]domain.Metric, error) {
	rows, err := r.db.QueryContext(ctx, `
		select m.run_id, m.timestamp, m.metric_name, m.value
		from metrics m
		join runs r on r.id = m.run_id
		where ($1 = '' or r.tenant = $1)
		  and ($2 = '' or r.model_name = $2)
		  and ($3 = '' or m.run_id = $3)
		  and ($4 = '' or m.metric_name = $4)
		  and ($5::timestamptz is null or m.timestamp >= $5)
		  and ($6::timestamptz is null or m.timestamp <= $6)
		order by m.timestamp
	`, filter.Tenant, filter.ModelName, filter.RunID, filter.MetricName, nullTime(filter.From), nullTime(filter.To))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			r.logger.ErrorContext(ctx, "failed to close metrics rows", "error", err)
		}
	}()
	var out []domain.Metric
	for rows.Next() {
		var metric domain.Metric
		if err := rows.Scan(&metric.RunID, &metric.Timestamp, &metric.MetricName, &metric.Value); err != nil {
			return nil, err
		}
		out = append(out, metric)
	}
	return out, rows.Err()
}

func (r *Repo) ListActiveRuns(ctx context.Context) ([]domain.Run, error) {
	rows, err := r.db.QueryContext(ctx, `
		select id, tenant, model_name, s3_path, model_format, target_rps, duration_seconds, max_latency_ms, payload, protocol, status, failure_reason, created_at, updated_at
		from runs where status IN ('PENDING', 'RUNNING') order by created_at
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			r.logger.ErrorContext(ctx, "failed to close active runs rows", "error", err)
		}
	}()
	var out []domain.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(...any) error
}

func scanRun(s scanner) (domain.Run, error) {
	var run domain.Run
	var rps sql.NullInt32
	var seconds sql.NullInt64
	var latency sql.NullInt32
	if err := s.Scan(&run.ID, &run.Tenant, &run.ModelName, &run.S3Path, &run.ModelFormat, &rps, &seconds, &latency, &run.Payload, &run.Protocol, &run.Status, &run.FailureReason, &run.CreatedAt, &run.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Run{}, app.ErrNotFound
		}
		return domain.Run{}, err
	}
	if rps.Valid {
		run.TargetRPS = &rps.Int32
	}
	if seconds.Valid {
		d := time.Duration(seconds.Int64) * time.Second
		run.Duration = &d
	}
	if latency.Valid {
		run.MaxLatencyMS = &latency.Int32
	}
	return run, nil
}

func affectOne(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
