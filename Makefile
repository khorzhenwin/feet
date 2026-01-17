.PHONY: all down infra-up infra-down infra-logs dev e2e test

ENV_FILE ?= config/env.example

all: dev

down:
	pkill -f "encore run" || true
	docker compose down

infra-up:
	docker compose up -d

infra-down:
	docker compose down

infra-logs:
	docker compose logs -f

dev: infra-up
	@set -a; \
	if [ -f "$(ENV_FILE)" ]; then . "$(ENV_FILE)"; fi; \
	set +a; \
	encore run

e2e:
	@./scripts/run-e2e.sh

test:
	encore test ./...
