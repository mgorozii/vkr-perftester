#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/lib.sh"

NS="${MODELMESH_NS:-modelmesh-serving}"
MINIO_PORT="${MINIO_PORT:-19000}"
MINIO_URL="http://127.0.0.1:${MINIO_PORT}"
MODEL_FILE="${MODEL_FILE:-${ROOT}/resnet50.onnx}"
BUCKET="${BUCKET:-modelmesh-example-models}"
OBJECT_PATH="${OBJECT_PATH:-${MODEL_PATH}}"

[[ -f "$MODEL_FILE" ]] || {
  echo "missing model file: $MODEL_FILE" >&2
  exit 1
}

cleanup() {
  [[ -n "${PF_PID:-}" ]] || return 0
  kill "$PF_PID" >/dev/null 2>&1 || true
  wait "$PF_PID" >/dev/null 2>&1 || true
}
trap cleanup EXIT

kubectl port-forward -n "$NS" svc/minio "${MINIO_PORT}:9000" >"${RUN_DIR}/pf-minio.log" 2>&1 &
PF_PID=$!

wait_http "${MINIO_URL}/minio/health/live" 120 1

aws_env=(
  AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-AKIAIOSFODNN7EXAMPLE}"
  AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY}"
  AWS_REGION="${AWS_REGION:-us-south}"
)

mb_err="$(env "${aws_env[@]}" s5cmd --endpoint-url "$MINIO_URL" mb "s3://${BUCKET}" 2>&1)" || {
  [[ "$mb_err" == *"BucketAlreadyExists"* || "$mb_err" == *"BucketAlreadyOwnedByYou"* ]] || {
    printf '%s\n' "$mb_err" >&2
    exit 1
  }
}

env "${aws_env[@]}" s5cmd --endpoint-url "$MINIO_URL" cp "$MODEL_FILE" "s3://${BUCKET}/${OBJECT_PATH}"

env "${aws_env[@]}" s5cmd --endpoint-url "$MINIO_URL" ls "s3://${BUCKET}/${OBJECT_PATH}"

log "uploaded ${MODEL_FILE##*/} to s3://${BUCKET}/${OBJECT_PATH}"
