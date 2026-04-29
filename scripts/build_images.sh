#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/lib.sh"

log "building service image"
docker build -t loadtestd:dev "$ROOT"

build_args=()
local_xk6_dir="${XK6_INFERENCE_LOCAL_DIR:-${ROOT}/../xk6-inference}"
if [[ "${USE_LOCAL_XK6_INFERENCE:-1}" != "0" && -d "$local_xk6_dir" ]]; then
	log "building k6 image with local xk6-inference: $local_xk6_dir"
	build_args+=(--build-context "xk6-inference-local=$local_xk6_dir")
else
	log "building k6 image with remote xk6-inference source"
fi

if [[ -n "${EXTENSION_SOURCE:-}" ]]; then
	build_args+=(--build-arg "EXTENSION_SOURCE=$EXTENSION_SOURCE")
fi

log "building k6 image"
docker build "${build_args[@]}" -f Dockerfile.k6 -t load-k6:dev "$ROOT"
