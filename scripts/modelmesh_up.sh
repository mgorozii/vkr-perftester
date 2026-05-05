#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/lib.sh"

ns="${MODELMESH_NS:-modelmesh-serving}"
repo="${ROOT}/.modelmesh-serving-repo"

kubectl get ns "$ns" >/dev/null 2>&1 || kubectl create ns "$ns"
if ! kubectl -n "$ns" get deploy modelmesh-controller >/dev/null 2>&1; then
  [[ -d "$repo" ]] || git clone -b main --depth 1 https://github.com/kserve/modelmesh-serving.git "$repo"
  (cd "$repo" && ./scripts/install.sh --namespace "$ns" --quickstart --enable-self-signed-ca)
fi
until kubectl get clusterservingruntime triton-2.x >/dev/null 2>&1; do sleep 2; done
kubectl apply -n "$ns" -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: model-serving-config
data:
  config.yaml: |
    restProxy:
      resources:
        requests: {cpu: 50m, memory: 96Mi}
        limits: {cpu: "2", memory: 2Gi}
    modelMeshResources:
      requests: {cpu: 300m, memory: 448Mi}
      limits: {cpu: "2", memory: 2Gi}
EOF
kubectl -n "$ns" create secret generic storage-config \
  --from-literal=localMinIO='{"type":"s3","access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_access_key":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY","endpoint_url":"http://minio.modelmesh-serving.svc.cluster.local:9000","region":"us-south","bucket":"modelmesh-example-models"}' \
  --dry-run=client -o yaml | kubectl apply -f -
