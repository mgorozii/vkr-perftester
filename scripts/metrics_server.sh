#!/usr/bin/env bash
set -euo pipefail

kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.8.1/components.yaml
kubectl -n kube-system patch deploy metrics-server --type=strategic --patch '
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
kubectl rollout status -n kube-system deploy/metrics-server --timeout=300s
kubectl wait --for=condition=Available apiservice/v1beta1.metrics.k8s.io --timeout=300s
