.PHONY: up down test lint start-search start-fixed

up:
	tilt up

down:
	tilt down
	kubectl delete ns loadtest-system modelmesh-serving --ignore-not-found=true
	kubectl get ns -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' | grep '^demo-' | xargs -r kubectl delete ns --ignore-not-found=true || true

test:
	go test -race -v ./...

lint:
	golangci-lint run --fix ./...

start-search:
	./scripts/start_search.sh

start-fixed:
	./scripts/start_fixed.sh
