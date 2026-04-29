alter table runs add column if not exists failure_reason text not null default '';
alter table search_steps add column if not exists error text not null default '';
