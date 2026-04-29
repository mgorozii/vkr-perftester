#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/lib.sh"

cd "$ROOT"

log "rebuilding images..."
./scripts/build_images.sh

log "restarting loadtestd..."
kubectl rollout restart deployment/loadtestd -n loadtest-system
kubectl rollout status deployment/loadtestd -n loadtest-system --timeout=300s

log "re-establishing port-forwarding..."
./scripts/port_forward_system.sh

log "starting search test..."
./scripts/start_search.sh

id_search="$(cat "${RUN_DIR}/last_run_id_search")"

log "search test report: http://127.0.0.1:8080/api/v1/grafana/report.html?run_id=${id_search}"
log "grafana: http://127.0.0.1:3000/d/load-tests/load-tests?orgId=1"

