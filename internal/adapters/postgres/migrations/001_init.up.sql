create table if not exists runs (
  id text primary key,
  tenant text not null,
  model_name text not null,
  model_format text not null default 'onnx',
  s3_path text not null,
  target_rps integer,
  duration_seconds integer,
  max_latency_ms integer,
  payload text not null default '',
  protocol text not null,
  status text not null,
  created_at timestamptz not null,
  updated_at timestamptz not null
);

create table if not exists metrics (
  run_id text not null references runs(id) on delete cascade,
  timestamp timestamptz not null,
  metric_name text not null,
  value double precision not null
);

create table if not exists search_steps (
  run_id text not null references runs(id) on delete cascade,
  step_number integer not null,
  rps integer not null,
  actual_rps float not null default 0,
  duration_seconds integer not null,
  p99_latency_ms float not null,
  error_rate float not null,
  stop_reason text not null default '',
  vus double precision not null default 0,
  max_vus double precision not null default 0,
  primary key (run_id, step_number)
);

create index if not exists metrics_run_metric_ts_idx on metrics(run_id, metric_name, timestamp);
create index if not exists runs_tenant_model_idx on runs(tenant, model_name, created_at desc);
