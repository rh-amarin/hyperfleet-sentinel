# Development Guide

> **Audience:** Developers building and testing Sentinel locally.

## Prerequisites

- Go 1.25+
- Docker or Podman
- Make
- pre-commit

## Setup

```bash
git clone https://github.com/openshift-hyperfleet/hyperfleet-sentinel.git
cd hyperfleet-sentinel
make generate       # generate OpenAPI client (required before build/test)
make download        # fetch Go dependencies
make build           # build bin/sentinel
make test            # run unit tests
make install-hooks   # install pre-commit hooks
```

> The OpenAPI client in `pkg/api/openapi/` is **not committed to git** — `make generate` must run before any build or test. See [openapi/README.md](../openapi/README.md) for spec version upgrades and generator details.

## Make Targets

| Target | Description |
|--------|-------------|
| `make generate` | Generate OpenAPI client from spec (Docker/Podman) |
| `make build` | Build `bin/sentinel` binary |
| `make test` | Unit tests with coverage |
| `make test-unit` | Unit tests only (specific packages) |
| `make test-integration` | Integration tests with testcontainers (Docker required) |
| `make test-all` | Unit + integration + Helm tests + lint |
| `make test-helm` | Helm chart lint + template validation |
| `make fmt` | Format Go code |
| `make lint` | Run golangci-lint |
| `make verify` | go vet + format check (fast) |
| `make clean` | Remove build artifacts and generated code |
| `make image` | Build container image |
| `make help` | Show all available targets |

Quick feedback loop: `make verify && make test-unit`

## Testing

- **Unit tests**: Fast, isolated, use mock implementations. Run with `make test`.
- **Integration tests**: End-to-end with real RabbitMQ/Pub/Sub via testcontainers. Run with `make test-integration`. See [testcontainers.md](testcontainers.md) for Docker/Podman setup and troubleshooting.
- **Helm tests**: Chart linting and template validation across 10 scenarios. Run with `make test-helm`.

## Running Locally

1. Start a message broker:

   ```bash
   # RabbitMQ (recommended for local dev)
   podman run -d --name rabbitmq -p 5672:5672 -p 15672:15672 rabbitmq:3-management
   ```

2. Configure broker credentials:

   ```bash
   export BROKER_RABBITMQ_URL="amqp://guest:guest@localhost:5672/"
   export HYPERFLEET_BROKER_TOPIC=hyperfleet-dev-${USER}-clusters
   ```

3. Run Sentinel:

   ```bash
   ./bin/sentinel serve --config=configs/dev-example.yaml
   ```

4. Verify:

   ```bash
   curl http://localhost:8080/healthz   # liveness
   curl http://localhost:8080/readyz    # readiness (503 until first poll)
   curl http://localhost:9090/metrics | grep hyperfleet_sentinel
   ```

For Pub/Sub emulator setup, broker.yaml configuration, and detailed logging options, see the [broker library documentation](https://github.com/openshift-hyperfleet/hyperfleet-broker).

To simulate HyperFleet API responses, use the [mock HyperFleet API](../test/mock-hyperfleet-api/).

## Container Image

```bash
# Build for local architecture
make image

# Build for AMD64 (required for GKE)
podman build --platform linux/amd64 -t <registry>/sentinel:<tag> .
```
