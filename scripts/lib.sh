#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_DIR="${ROOT}/.run"
mkdir -p "$RUN_DIR"

export MODEL_NAME="${MODEL_NAME:-resnet50}"
export MODEL_PATH="${MODEL_PATH:-onnx/resnet50.onnx}"

log() {
  printf '[load] %s\n' "$*"
}

pid_kill() {
  local file="$1"
  [[ -f "$file" ]] || return 0
  local pid
  pid="$(cat "$file")"
  if kill -0 "$pid" >/dev/null 2>&1; then
    kill "$pid" >/dev/null 2>&1 || true
    wait "$pid" >/dev/null 2>&1 || true
  fi
  rm -f "$file"
}

wait_http() {
  local url="$1"
  local tries="${2:-120}"
  local delay="${3:-1}"
  for ((i=0;i<tries;i++)); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$delay"
  done
  return 1
}

kill_port() {
  local port="$1"
  lsof -ti tcp:"$port" | xargs kill >/dev/null 2>&1 || true
}

wait_test() {
  local id="$1"
  for _ in $(seq 1 1200); do
    status="$(curl -fsS "http://127.0.0.1:8080/api/v1/runs/${id}" | jq -r '.status')"
    case "$status" in
      SUCCESS) log "run ${id} finished"; return 0 ;;
      FAILED) log "run ${id} failed"; return 1 ;;
    esac
    sleep 5
  done
  log "run ${id} timed out"
  return 1
}

# ResNet-50 payload: 1x3x224x224 = 150528 floats
get_resnet_payload() {
  python3 -c "import json; print(json.dumps({'inputs': [{'name': 'input', 'shape': [1, 3, 224, 224], 'datatype': 'FP32', 'data': [0.5]*150528}]}))"
}
