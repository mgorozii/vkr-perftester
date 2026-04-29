#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/lib.sh"

log "starting fixed RPS test"
payload_json=$(get_resnet_payload)

req=$(jq -n \
  --arg tenant "demo" \
  --arg name "$MODEL_NAME" \
  --arg s3_path "s3://modelmesh-example-models/$MODEL_PATH" \
  --arg format "onnx" \
  --arg rps "10" \
  --arg duration "30s" \
  --arg protocol "HTTP" \
  --arg payload "$payload_json" \
  '{tenant: $tenant, name: $name, s3_path: $s3_path, model_format: $format, target_rps: ($rps|tonumber), duration: $duration, protocol: $protocol, payload: $payload}')

run_id="$(echo "$req" | curl -fsS -X POST http://127.0.0.1:8080/api/v1/tests:start -H 'content-type: application/json' -d @- | jq -r '.run_id')"
echo "$run_id" > "${RUN_DIR}/last_run_id_fixed"
wait_test "$run_id"
