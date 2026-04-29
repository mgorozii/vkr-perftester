#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/lib.sh"

cd "$ROOT"

make lint && make test

"${ROOT}/scripts/build_images.sh"
"${ROOT}/scripts/modelmesh_up.sh"
"${ROOT}/scripts/system_up.sh"
"${ROOT}/scripts/port_forward_system.sh"
"${ROOT}/scripts/start_search.sh"

id_search="$(cat "${RUN_DIR}/last_run_id_search")"

log "search test report: http://127.0.0.1:8080/api/v1/grafana/report.html?run_id=${id_search}"
log "grafana: http://127.0.0.1:3000/d/load-tests/load-tests?orgId=1"
