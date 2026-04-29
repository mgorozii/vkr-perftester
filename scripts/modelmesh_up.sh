#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/lib.sh"

NS="modelmesh-serving"
REPO_DIR="${ROOT}/.modelmesh-serving-repo"

if ! kubectl get ns "$NS" >/dev/null 2>&1; then
  kubectl create namespace "$NS"
fi

if ! kubectl -n "$NS" get deploy modelmesh-controller >/dev/null 2>&1; then
  log "installing modelmesh"
  if [ ! -d "$REPO_DIR" ]; then
    git clone -b main --depth 1 https://github.com/kserve/modelmesh-serving.git "$REPO_DIR"
  fi
  (
    cd "$REPO_DIR"
    ./scripts/install.sh --namespace "$NS" --quickstart --enable-self-signed-ca
  )
fi

log "waiting for triton-2.x clusterservingruntime to be created"
while true; do
  if kubectl get clusterservingruntime triton-2.x >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

log "configuring rest-proxy and modelmesh sidecar resources"
kubectl apply -n "$NS" -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: model-serving-config
data:
  config.yaml: |
    restProxy:
      resources:
        requests:
          cpu: "50m"
          memory: "96Mi"
        limits:
          cpu: "2"
          memory: "2Gi"
    modelMeshResources:
      requests:
        cpu: "300m"
        memory: "448Mi"
      limits:
        cpu: "2"
        memory: "2Gi"
EOF

log "configuring storage secret with FQDN"
kubectl -n "$NS" create secret generic storage-config \
  --from-literal=localMinIO='{
    "type": "s3",
    "access_key_id": "AKIAIOSFODNN7EXAMPLE",
    "secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
    "endpoint_url": "http://minio.modelmesh-serving.svc.cluster.local:9000",
    "region": "us-south",
    "bucket": "modelmesh-example-models"
  }' \
  --dry-run=client -o yaml | kubectl apply -f -

log "uploading ${MODEL_NAME} model to minio"
"${ROOT}/scripts/upload_resnet.sh"

log "deploying sample model"
envsubst < "${ROOT}/k8s/triton-model.yaml" | kubectl apply -n "$NS" -f -

log "waiting for inferenceservice/${MODEL_NAME} to be ready..."
(
  while true; do
    kubectl get pods -n "$NS" | grep -E "modelmesh-serving|model-name" || true
    kubectl get isvc "${MODEL_NAME}" -n "$NS" -o custom-columns=NAME:.metadata.name,READY:.status.modelStatus.states.activeModelState,URL:.status.url || true
    sleep 10
  done
) &
LOOP_PID=$!

if kubectl wait --for=condition=Ready "inferenceservice/${MODEL_NAME}" -n "$NS" --timeout=900s; then
  log "model ${MODEL_NAME} is ready"
else
  log "model ${MODEL_NAME} failed to become ready"
  kubectl describe isvc "${MODEL_NAME}" -n "$NS"
  kill $LOOP_PID
  exit 1
fi
kill $LOOP_PID
