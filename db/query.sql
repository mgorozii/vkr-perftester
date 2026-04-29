-- name: CreateRun :exec
insert into runs (id, tenant, model_name, s3_path, model_format, target_rps, duration_seconds, payload, protocol, status, created_at, updated_at)
values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12);

-- name: GetRun :one
select * from runs where id = $1;

-- name: ListRuns :many
select * from runs
where ($1 = '' or tenant = $1)
  and ($2 = '' or model_name = $2)
  and ($3 = '' or id = $3)
order by created_at desc;

-- name: UpdateRunStatus :exec
update runs set status = $2, updated_at = $3 where id = $1;

-- name: SaveMetric :exec
insert into metrics (run_id, timestamp, metric_name, value) values ($1,$2,$3,$4);

-- name: ListMetrics :many
select m.* from metrics m
join runs r on r.id = m.run_id
where ($1 = '' or r.tenant = $1)
  and ($2 = '' or r.model_name = $2)
  and ($3 = '' or m.run_id = $3)
  and ($4 = '' or m.metric_name = $4)
  and ($5::timestamptz is null or m.timestamp >= $5)
  and ($6::timestamptz is null or m.timestamp <= $6)
order by m.timestamp;

-- name: ListActiveRuns :many
select * from runs where status IN ('PENDING', 'RUNNING') order by created_at;
