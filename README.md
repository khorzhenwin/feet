# Fees Service

Fees API built with Encore and Temporal. Bills accrue line items during a billing period and are closed automatically by a Temporal workflow or manually via API.

## Prerequisites
- Encore CLI
- Docker (for Postgres + Temporal)

Install Encore:
- **macOS:** `brew install encoredev/tap/encore`
- **Linux:** `curl -L https://encore.dev/install.sh | bash`
- **Windows:** `iwr https://encore.dev/install.ps1 | iex`

## Local Development

### Configure env
Copy the example variables and adjust as needed:

```
config/env.example
```

Key vars:
- `TEMPORAL_ADDRESS` (default `localhost:7233`)
- `TEMPORAL_NAMESPACE` (default `default`)
- `FEE_PERIOD` (duration string, e.g. `10s` for testing)

### Start infra + app
```
make dev
```

This:
- boots Postgres + Temporal via Docker Compose
- loads env from `config/env.example`
- runs `encore run`

### Start everything
```
make all
```

### Useful URLs
- API base: `http://localhost:4000`
- Encore dev dashboard: `http://localhost:9400`
- Temporal UI: `http://localhost:8080`

## API Summary
- `POST /bills` create a bill and start the workflow
- `POST /bills/:bill_id/line-items` add a line item
- `POST /bills/:bill_id/close` manually close a bill
- `GET /bills/:bill_id` fetch bill details
- `GET /bills?status=open|closed|charged` list bills

## Architecture Diagram
```mermaid
flowchart TD
  Client[Client / Other Services] -->|REST| FeesAPI[Fees Service (Encore)]
  FeesAPI -->|SQL| FeesDB[(Fees Postgres)]
  FeesAPI -->|Start Workflow| Temporal[Temporal Server]
  Temporal -->|Activity: create bill| FeesDB
  Temporal -->|Timer: FEE_PERIOD| Temporal
  Temporal -->|Activity: close bill| FeesDB
  FeesAPI -->|Signal: bill-closed| Temporal
```

## E2E Tests
Run end-to-end tests against a running instance:

```
make e2e
```

The script expects:
- `E2E_BASE_URL` (defaults to `http://localhost:4000`)
- Infra running via `make infra-up`
- App running via `make dev` (or your own `encore run`)

## Other Commands
```
make all
make infra-up
make infra-down
make test
```