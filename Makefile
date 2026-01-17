.PHONY: all down infra-up infra-down infra-logs dev e2e test seed

ENV_FILE ?= config/env.example
SEED_WAIT ?= 10

all: infra-up
	@set -a; \
	if [ -f "$(ENV_FILE)" ]; then . "$(ENV_FILE)"; fi; \
	set +a; \
	encore run &
	@sleep $(SEED_WAIT)
	@$(MAKE) seed

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

seed:
	curl -s -X POST http://localhost:4000/dev/seed | cat
