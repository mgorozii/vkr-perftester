.PHONY: test lint build images infra start full down

test:
	go test -race -v ./...

lint:
	golangci-lint run --fix ./...

build:
	go build ./...

images:
	./scripts/build_images.sh

infra:
	./scripts/modelmesh_up.sh
	./scripts/system_up.sh

start:
	./scripts/port_forward_system.sh
	./scripts/start_test.sh

full:
	./scripts/full.sh

down:
	./scripts/down.sh
