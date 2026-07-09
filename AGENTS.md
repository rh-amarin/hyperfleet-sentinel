# CLAUDE.md

## Project Identity

HyperFleet Sentinel is a **Kubernetes resource watcher** that polls the HyperFleet API for cluster/nodepool updates, makes orchestration decisions via CEL-based decision logic, and publishes CloudEvents to message brokers. Stateless, horizontally scalable via label-based sharding, delegates all state persistence to the API.

- **Language**: Go 1.25 (see `go.mod`)
- **Messaging**: Broker abstraction (RabbitMQ, GCP Pub/Sub, Stub)
- **API Client**: Generated from [hyperfleet-api-spec](https://github.com/openshift-hyperfleet/hyperfleet-api-spec) — see [openapi/README.md](openapi/README.md)
- **Deployment**: Helm chart in `charts/`

Sentinel is one component in the HyperFleet control plane:
- **API** — persists cluster/nodepool state (source of truth)
- **Sentinel** — watches API, decides when resources need reconciliation, publishes events
- **Adapters** — consume events, execute provisioning/deprovisioning, report back to API
- **Broker** (RabbitMQ or Pub/Sub) — decouples Sentinel from adapters

## Critical First Steps

**Generated OpenAPI client is NOT committed to git.** Before any build, test, or development task:

```bash
make generate    # Extracts OpenAPI spec from hyperfleet-api-spec module and generates Go client
```

Setup sequence for a fresh clone:
1. `make generate` — generate OpenAPI client in `pkg/api/openapi/`
2. `make download` — fetch Go dependencies
3. `make install-hooks` — install pre-commit hooks (secret scanning, linting, etc.)
4. `make build` — build `bin/sentinel` binary
5. `make test` — verify unit tests pass

## Verification

| Command | What it does |
|---|---|
| `make verify` | go vet + format check (fast) |
| `make lint` | golangci-lint (comprehensive) |
| `make test` | all tests (`./...`), writes `coverage.out` profile |
| `make test-unit` | unit tests only — specific internal/ and pkg/ packages |
| `make test-integration` | integration tests with testcontainers (Docker required) |
| `make test-coverage` | runs `make test` then opens HTML coverage report |
| `make test-helm` | Helm chart lint + template validation (10 scenarios) |
| `make test-all` | test + test-integration + test-helm + lint |

Quick feedback: `make verify && make test-unit`. Full pre-push: `make test-all`.

**PR pre-flight order:**
1. `make generate`
2. `make fmt`
3. `make lint`
4. `make test-unit`
5. `make test-integration` — if broker/API changes
6. `make test-helm` — if chart changes
7. Update CHANGELOG.md if the change is user-visible

## Source of Truth

| Topic | Where to look |
|---|---|
| Configuration reference | [docs/config.md](docs/config.md) |
| Metrics definitions | [docs/metrics.md](docs/metrics.md), `internal/metrics/` |
| Development setup | [docs/development.md](docs/development.md) |
| Helm deployment | [docs/deployment.md](docs/deployment.md) |
| GKE dev deployment | [docs/sentinel-for-gke-dev.md](docs/sentinel-for-gke-dev.md) for GKE dev deployment |
| Multi-instance sharding | [docs/multi-instance-deployment.md](docs/multi-instance-deployment.md) |
| Alerts and runbooks | [docs/alerts.md](docs/alerts.md), [docs/runbook.md](docs/runbook.md) |
| Helm values | [charts/values.yaml](charts/values.yaml) |
| Contributing and setup | [CONTRIBUTING.md](CONTRIBUTING.md) |
| OpenAPI client generation | [openapi/README.md](openapi/README.md) |
| Example configs | `configs/dev-example.yaml`, `configs/rabbitmq-example.yaml`, `configs/gcp-pubsub-example.yaml` |
| Broker configuration | `broker.yaml` (loaded by hyperfleet-broker; override path via `BROKER_CONFIG_FILE` env var) |
| CloudEvents / CEL payloads | `internal/payload/` |
| Resource profiling | [docs/resource-profiling.md](docs/resource-profiling.md) |

## Architecture Context

Sentinel's job: **decide when**, not **execute how**. It can be killed and restarted at any time without data loss — this is what makes label-based sharding safe. The `message_decision` config uses CEL expressions to decide when to publish — see `DefaultMessageDecision()` in `internal/config/config.go` for default expressions.

### Key Internal Patterns
- **Config validation fails fast** — `Validate()` returns error at startup, `LoadConfig()` propagates to main which exits non-zero
- **Context propagation** — `context.Context` threaded through all calls with correlation keys (OpID, TraceID, SpanID, DecisionReason)
- **Health probes** — `/healthz` (liveness: stale poll detection), `/readyz` (readiness: broker + first successful poll)

## Code Conventions

### Commit Messages
Format: `HYPERFLEET-### - type: description`

Example:
```
HYPERFLEET-427 - feat: add standard metrics labels

Adds resource_type and resource_selector labels to all
Prometheus metrics for consistent querying.

Co-Authored-By: Claude <noreply@anthropic.com>
```
Co-Authored-By trailer required on all Claude-assisted commits.

### Configuration
- Config struct in `internal/config/config.go` — YAML struct tags, validation via `Validate()`
- All durations use `time.Duration` with YAML `duration` format (e.g., `5s`, `30m`)
- Config precedence (highest wins): CLI flags > env vars (`HYPERFLEET_*`) > YAML file > defaults
- Broker credentials handled separately via `broker.yaml` (or `BROKER_CONFIG_FILE` env var)

### CLI Commands
- `sentinel serve --config config.yaml` — run the service
- `sentinel config-dump --config config.yaml` — print merged config (debug precedence issues)
- `sentinel version` — print version, commit, build date
- Run `sentinel serve --help` for full flag list

### Error Handling
- Log at boundaries (main service loop), not deep in call stack

### Logging
- Custom structured logger in `pkg/logger/` — stdlib only, no external deps
- Interface: `logger.HyperFleetLogger` with `Info()`, `Error()`, `Warn()`, `Debug()`, `V(level)` (verbosity), `Extra()`
- Create via `logger.NewHyperFleetLogger()` — uses global config
- Chaining: `logger.Extra("key", val).Extra("key2", val2).Info("msg")`
- **IMPORTANT: always use `pkg/logger`, never `log/slog` directly**

### CloudEvents Payloads
`message_data` config uses CEL expressions, not static values:
```yaml
message_data:
  id: resource.id
  kind: resource.kind
  href: resource.href
```
CEL context:
- `resource` — cluster/nodepool object from API (id, kind, href, generation, status, labels, etc.)
- `reason` — decision reason string from engine (e.g., `"message decision matched"`, `"message decision result is false"`)
- `condition("Type")` — custom function to look up resource status condition by type name
- `now` — current timestamp
- `timestamp()`, `duration()` — standard CEL time functions

### Testing
- Table-driven tests with plain `if` assertions — no testify
- Mocking via simple interface implementations (e.g., MockPublisher), no gomock
- Unit tests live alongside code: `foo_test.go` next to `foo.go`
- Integration tests in `test/integration/` with `//go:build integration` tag
- Prometheus metrics verified with `prometheus/testutil`
- Run single test: `go test -run TestDecisionEngine ./internal/engine/...`

## Git Workflow

- Branch from `main`, PR back to `main`
- Branch naming: `HYPERFLEET-###-short-description`
### Pre-commit Hooks
Install: `make install-hooks`

Hooks:
- `leaktk.git.pre-commit` — secret scanning (open-source, no VPN required)
- `hyperfleet-commitlint` — validates commit message format (commit-msg stage)
- `hyperfleet-gofmt` — Go code formatting
- `hyperfleet-golangci-lint` — linting
- `hyperfleet-go-vet` — Go vet checks
- `trailing-whitespace` — removes trailing whitespace
- `end-of-file-fixer` — ensures files end with newline
- `check-added-large-files` — prevents large files from being committed

## Project Boundaries

**DO NOT**:
- Add business logic to Sentinel — orchestration decisions only, execution belongs in adapters
- Store state in Sentinel — it is stateless, API is source of truth
- Hardcode the resource polling interval — always use `poll_interval` from config for the main sentinel loop; adding a second resource polling loop bypasses the single-ticker backpressure model

**DO**:
- Update `hyperfleet-api-spec` version in `go.mod` and run `make generate` when API spec changes
- New exported functions require unit tests; new broker/API interactions require integration tests
- Add metrics when adding observable behavior — see [docs/metrics.md](docs/metrics.md) for conventions
- Convention: `message_data` should include `id`, `kind`, `href` fields (not enforced by validation, but expected by downstream adapters) — see `configs/dev-example.yaml`
- Use broker abstraction (`hyperfleet-broker`) — never import RabbitMQ/Pub/Sub clients directly

## Gotchas

- **`make generate` is mandatory** — build and tests fail without it; generated code is gitignored
- **`pkg/api/openapi/` is read-only** — never hand-edit, always regenerate
- **Broker config comes from `broker.yaml`** (or `BROKER_CONFIG_FILE` env var), not sentinel YAML config — handled by hyperfleet-broker library
- **CEL expressions in `message_data` are compiled at startup** — syntax errors fail fast, but semantic errors (wrong field names on resource) surface at evaluation time
- **Metrics labels must include `resource_type` and `resource_selector`** — see [docs/metrics.md](docs/metrics.md) for naming conventions
- **Metrics use `sync.Once` registration** — call `ResetSentinelMetrics()` in tests to avoid duplicate registration panics
- **No testify** — project uses plain Go assertions and table-driven tests; don't introduce testify
