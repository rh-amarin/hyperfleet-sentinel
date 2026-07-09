# hyperfleet-sentinel

Kubernetes service that polls the HyperFleet API for cluster and nodepool updates, makes orchestration decisions via CEL-based decision logic, and publishes CloudEvents to message brokers. Stateless, horizontally scalable via label-based sharding, delegates all state persistence to the API.

## Quick Start

### Deploy the Full Stack

Sentinel requires a running message broker and HyperFleet API as well as adapters. The [hyperfleet-infra](https://github.com/openshift-hyperfleet/hyperfleet-infra) repository provides a one-command setup that deploys the complete HyperFleet stack including pre-configured adapters:

| Command | What it deploys |
|---------|-----------------|
| `make local-up-gcp` | GKE cluster + images + API + adapters + Maestro |
| `make install-hyperfleet` | Everything on an existing K8s cluster using RabbitMQ (no GCP needed) |
| `make install-adapters` | Install sample HyperFleet Adapters only |
| `make install-api` | Install sample HyperFleet API only |
| `helm install rabbitmq ./helm/rabbitmq` | Install a simple RabbitMQ instance |
| `make status` | Verify the deployment |

Make sure you define the following environment variables:
* `HELMFILE_ENV`: accepted values: `kind`, `gcp`
* `NAMESPACE`: namespace where HyperFleet components will be deployed
* `REGISTRY`: The registry namespace from which to pull the images. `quay.io/openshift-hyperfleet` for released images
* `API_IMAGE_TAG`: image tag for `hyperfleet-api` container image
* `SENTINEL_IMAGE_TAG`: image tag for `hyperfleet-sentinel` container image
* `ADAPTER_IMAGE_TAG`: image tag for `hyperfleet-adapter` container image

See [hyperfleet-infra](https://github.com/openshift-hyperfleet/hyperfleet-infra) for required environment variables and full instructions.

### Try Locally

```bash
make generate && make build
export BROKER_RABBITMQ_URL="amqp://guest:guest@localhost:5672/"
export HYPERFLEET_BROKER_TOPIC=hyperfleet-dev-clusters
./bin/sentinel serve --config=configs/dev-example.yaml
```

See the [Development Guide](docs/development.md) for full setup instructions.

## Documentation

### For Operators (deploying and running Sentinel)

| Guide | Description |
|-------|-------------|
| [Deployment Guide](docs/deployment.md) | Helm deployment, broker setup, examples |
| [Helm Values Reference](charts/README.md) | Complete chart values |
| [Configuration Reference](docs/config.md) | YAML schema, CLI flags, env vars |
| [Operator Guide](docs/sentinel-operator-guide.md) | Decision engine, CEL expressions, concepts |
| [Scaling](docs/multi-instance-deployment.md) | Horizontal sharding patterns |
| [Resource Sizing](docs/resource-profiling.md) | CPU/memory at scale |

### For SREs (monitoring and operations)

| Guide | Description |
|-------|-------------|
| [Metrics](docs/metrics.md) | Metric catalog, PromQL examples, Grafana |
| [Alerts](docs/alerts.md) | Alert rules, severity, response |
| [Runbook](docs/runbook.md) | Reliability features, health checks, failure recovery |

### For Developers

| Guide | Description |
|-------|-------------|
| [Development Guide](docs/development.md) | Setup, build, test, run locally |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Commit standards, PR workflow |
| [Integration Tests](docs/testcontainers.md) | Testcontainer setup |

### Architecture

| Resource | Description |
|----------|-------------|
| [HyperFleet Architecture](https://github.com/openshift-hyperfleet/architecture) | System design |
| [HyperFleet API Spec](https://github.com/openshift-hyperfleet/hyperfleet-api-spec) | API contract |
| [Broker Library](https://github.com/openshift-hyperfleet/hyperfleet-broker) | Messaging abstraction |
| [Infrastructure](https://github.com/openshift-hyperfleet/hyperfleet-infra) | Deployment automation |

## CLI Reference

| Command | Description |
|---------|-------------|
| `sentinel serve --config config.yaml` | Run the service |
| `sentinel config-dump --config config.yaml` | Print merged configuration |
| `sentinel version` | Print version, commit, build date |

Run `sentinel serve --help` for the full flag list.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [Development Guide](docs/development.md).

## Repository Access

All members of the **hyperfleet** team have write access to this repository.

1. Verify you're a member of the `openshift-hyperfleet` organization
2. Confirm you're added to the hyperfleet team
3. Code reviews and approvals are managed through the OWNERS file

For access issues, contact a repository administrator or organization owner.
