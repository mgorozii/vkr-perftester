#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/lib.sh"

pid_kill "${RUN_DIR}/pf-grafana.pid"
pid_kill "${RUN_DIR}/pf-app.pid"
pid_kill "${RUN_DIR}/pf-jaeger.pid"
pkill -f 'kubectl port-forward -n loadtest-system svc/grafana 3000:3000' >/dev/null 2>&1 || true
pkill -f 'kubectl port-forward -n loadtest-system svc/loadtestd 8080:8080' >/dev/null 2>&1 || true
pkill -f 'kubectl port-forward -n loadtest-system svc/jaeger 16686:16686' >/dev/null 2>&1 || true
kill_port 3000
kill_port 8080
kill_port 16686

start_pf() {
  local target="$1" ports="$2" pid_file="$3" log_file="$4"
  : >"$log_file"
  LOG_FILE="$log_file" perl -MPOSIX=setsid -e '
    my @cmd=@ARGV;
    my $pid=fork();
    exit 0 if $pid;
    setsid() or die "setsid failed";
    $pid=fork();
    exit 0 if $pid;
    open STDIN, "<", "/dev/null" or die $!;
    open STDOUT, ">>", $ENV{LOG_FILE} or die $!;
    open STDERR, ">&STDOUT" or die $!;
    exec @cmd or die $!;
  ' kubectl port-forward -n loadtest-system "$target" "$ports"
  sleep 1
  pgrep -f "kubectl port-forward -n loadtest-system ${target} ${ports}" | tail -n 1 > "$pid_file"
  sleep 1
}

start_pf svc/grafana 3000:3000 "${RUN_DIR}/pf-grafana.pid" "${RUN_DIR}/pf-grafana.log"
start_pf svc/loadtestd 8080:8080 "${RUN_DIR}/pf-app.pid" "${RUN_DIR}/pf-app.log"
start_pf svc/jaeger 16686:16686 "${RUN_DIR}/pf-jaeger.pid" "${RUN_DIR}/pf-jaeger.log"

wait_http "http://127.0.0.1:3000/api/health" 120 1
wait_http "http://127.0.0.1:8080/healthz" 120 1
wait_http "http://127.0.0.1:16686" 120 1
