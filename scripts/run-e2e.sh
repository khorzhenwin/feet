#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT_DIR/config/env.example}"

if [[ -f "$ENV_FILE" ]]; then
  while IFS='=' read -r key value; do
    [[ -z "$key" || "$key" =~ ^# ]] && continue
    if [[ -z "${!key:-}" ]]; then
      export "$key"="$value"
    fi
  done < "$ENV_FILE"
fi

export E2E_BASE_URL="${E2E_BASE_URL:-http://localhost:4000}"

echo "Running e2e tests against ${E2E_BASE_URL}"
go test ./e2e -v
