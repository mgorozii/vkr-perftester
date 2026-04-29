#!/usr/bin/env bash
set -euo pipefail
. "$(dirname "$0")/lib.sh"

pid_kill "${RUN_DIR}/pf-grafana.pid"
pid_kill "${RUN_DIR}/pf-app.pid"
pkill -f 'kubectl port-forward -n loadtest-system svc/grafana 3000:3000' >/dev/null 2>&1 || true
pkill -f 'kubectl port-forward -n loadtest-system svc/loadtestd 8080:8080' >/dev/null 2>&1 || true
kill_port 3000
kill_port 8080
kubectl get ns -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' | egrep '^demo-' | xargs -r kubectl delete ns --ignore-not-found=true || true
kubectl delete ns modelmesh-serving --ignore-not-found=true || true
kubectl delete ns loadtest-system --ignore-not-found=true || true
rm -rf "${RUN_DIR}"
#rm -rf "${ROOT}/.modelmesh-serving-repo"
