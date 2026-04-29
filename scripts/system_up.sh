#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/lib.sh"

METRICS_SERVER_VERSION="${METRICS_SERVER_VERSION:-v0.8.1}"

ensure_metrics_server() {
  kubectl apply -f "https://github.com/kubernetes-sigs/metrics-server/releases/download/${METRICS_SERVER_VERSION}/components.yaml"
  kubectl -n kube-system patch deployment metrics-server --type=strategic --patch '
spec:
  template:
    spec:
      containers:
        - name: metrics-server
          args:
            - --cert-dir=/tmp
            - --secure-port=10250
            - --kubelet-use-node-status-port
            - --metric-resolution=15s
            - --kubelet-insecure-tls
            - --kubelet-preferred-address-types=InternalIP,Hostname
'
  kubectl rollout status deployment/metrics-server -n kube-system --timeout=300s
  kubectl wait --for=condition=Available apiservice/v1beta1.metrics.k8s.io --timeout=300s
}

ensure_metrics_server
kubectl apply -f "$ROOT/k8s/load-system"
kubectl wait --for=condition=available deployment/postgres -n loadtest-system --timeout=300s
kubectl wait --for=condition=available deployment/grafana -n loadtest-system --timeout=300s
kubectl wait --for=condition=available deployment/loadtestd -n loadtest-system --timeout=300s
