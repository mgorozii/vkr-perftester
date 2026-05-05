#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_DIR="${ROOT}/.run"
mkdir -p "$RUN_DIR"
MODEL_NAME="${MODEL_NAME:-resnet50}"
MODEL_PATH="${MODEL_PATH:-onnx/resnet50.onnx}"

log() {
  printf '[load] %s\n' "$*"
}

wait_http() {
  local url="$1" tries="${2:-120}" delay="${3:-1}"
  for _ in $(seq 1 "$tries"); do
    curl -fsS "$url" >/dev/null 2>&1 && return 0
    sleep "$delay"
  done
  return 1
}

wait_test() {
  local id="$1"
  log "waiting for run ${id}..."
  for _ in $(seq 1 1200); do
    status="$(curl -fsS "http://127.0.0.1:8080/api/v1/runs/${id}" | jq -r '.status')"
    case "$status" in
      SUCCESS) log "run ${id} finished successfully"; return 0 ;;
      FAILED) log "run ${id} failed"; return 1 ;;
    esac
    sleep 5
  done
  log "run ${id} timed out"
  return 1
}

get_resnet_payload() {
  python3 -c "import json; print(json.dumps({'inputs': [{'name': 'input', 'shape': [1, 3, 224, 224], 'datatype': 'FP32', 'data': [0.5]*150528}]}))"
}
