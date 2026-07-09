# Sentinel Configuration Reference

> **Audience:** Developers and operators configuring Sentinel.

This document describes all Sentinel configuration options and how to set them via YAML, command-line flags, and environment variables.

Overrides are applied in this order: **CLI flags > environment variables > YAML file > defaults**.

For decision engine concepts and CEL expression guidance, see the [Operator Guide](sentinel-operator-guide.md).

## Config File Location

| Method | Value |
|--------|-------|
| CLI flag | `--config` (or `-c`) |
| Environment variable | `HYPERFLEET_CONFIG` |
| Default | `/etc/hyperfleet/config.yaml` |

A config file must exist at the resolved path.

## Configuration File Examples

Example configurations are in the `configs/` directory:

- **`configs/dev-example.yaml`** — minimal development configuration
- **`configs/rabbitmq-example.yaml`** — RabbitMQ broker configuration
- **`configs/gcp-pubsub-example.yaml`** — GCP Pub/Sub broker configuration

## YAML Schema

All fields use **snake_case** naming.

### Required Fields

| Field | Type | Description | Example |
|-------|------|-------------|---------|
| `resource_type` | string | Resource type plural to watch (e.g. `clusters`, `nodepools`, `wifconfigs`) | `clusters` |
| `clients.hyperfleet_api.base_url` | string | HyperFleet API base URL | `http://hyperfleet-api:8000` |

### Optional Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `sentinel.name` | string | | Sentinel component name/identifier |
| `debug_config` | bool | `false` | Log merged config after load |
| `tracing_enabled` | bool | `false` | Enable OpenTelemetry distributed tracing |
| `poll_interval` | duration | `5s` | How often to poll the API |
| `resource_selector` | list | `[]` | Label selectors for filtering resources (enables sharding) |
| `message_decision` | object | See below | CEL-based decision logic |
| `message_data` | map | `{}` | CEL expressions defining the CloudEvent payload |
| `clients.hyperfleet_api.version` | string | `v1` | API version |
| `clients.hyperfleet_api.timeout` | duration | `10s` | HTTP client timeout |
| `clients.hyperfleet_api.page_size` | int | `20` | Number of resources per API page (1–500) |
| `clients.broker.topic` | string | | Broker topic for publishing events |
| `log.level` | string | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `log.format` | string | `json` | Log format (`json` or `text`) |
| `log.output` | string | `stdout` | Log output destination (`stdout`, `stderr`) |

### Resource Selector (Sharding)

The `resource_selector` field enables horizontal scaling by having multiple Sentinel instances watch different resource subsets:

```yaml
resource_selector:
  - label: shard
    value: "1"
  - label: region
    value: us-east-1
```

An empty or omitted `resource_selector` means watch all resources. Multiple selectors use AND logic (all labels must match).

For deployment patterns, see [Multi-Instance Deployment](multi-instance-deployment.md).

### Message Decision (CEL Decision Engine)

The `message_decision` field controls when Sentinel publishes events using CEL expressions:

```yaml
message_decision:
  params:
    - name: ref_time
      expr: 'condition("Reconciled").last_updated_time'
    - name: is_reconciled
      expr: 'condition("Reconciled").status == "True"'
    - name: has_ref_time
      expr: 'ref_time != ""'
    - name: is_new_resource
      expr: '!is_reconciled && resource.generation == 1'
    - name: generation_mismatch
      expr: 'resource.generation > condition("Reconciled").observed_generation'
    - name: reconciled_and_stale
      expr: 'is_reconciled && has_ref_time && now - timestamp(ref_time) > duration("30m")'
    - name: not_reconciled_and_debounced
      expr: '!is_reconciled && has_ref_time && now - timestamp(ref_time) > duration("10s")'
  result: "is_new_resource || generation_mismatch || reconciled_and_stale || not_reconciled_and_debounced"
```

`params` are named CEL expressions evaluated in dependency order. `result` is a boolean CEL expression using the params. For detailed CEL concepts and available variables, see the [Operator Guide](sentinel-operator-guide.md).

### Message Data (CloudEvent Payload)

Define custom fields for the CloudEvent data payload using CEL expressions:

```yaml
message_data:
  id: resource.id
  kind: resource.kind
  href: resource.href
  generation: resource.generation
```

CEL expressions have access to:
- `resource` — the resource object fetched from HyperFleet API
- `reason` — decision outcome string

### Broker Configuration

Broker implementation details (RabbitMQ URL, GCP project ID, etc.) are configured separately via `broker.yaml` or [hyperfleet-broker](https://github.com/openshift-hyperfleet/hyperfleet-broker) environment variables:

| Variable | Description | Example |
|----------|-------------|---------|
| `BROKER_RABBITMQ_URL` | RabbitMQ connection URL | `amqp://user:pass@localhost:5672/vhost` |
| `BROKER_GOOGLEPUBSUB_PROJECT_ID` | GCP project ID | `my-gcp-project` |
| `GOOGLE_APPLICATION_CREDENTIALS` | Service account key path (optional, uses ADC if not set) | `/path/to/key.json` |
| `HYPERFLEET_BROKER_TOPIC` | Topic name for publishing events | `hyperfleet-dev-clusters` |
| `BROKER_CONFIG_FILE` | Path to broker config file | `/etc/hyperfleet/broker.yaml` |

For Helm-based broker configuration, see the [Deployment Guide](deployment.md).

## Command-Line Flags

| Flag | Maps to YAML field |
|------|--------------------|
| `--config`, `-c` | Config file path |
| `--debug-config` | `debug_config` |
| `--tracing-enabled` | `tracing_enabled` |
| `--name` | `sentinel.name` |
| `--log-level` | `log.level` |
| `--log-format` | `log.format` |
| `--log-output` | `log.output` |
| `--hyperfleet-api-base-url` | `clients.hyperfleet_api.base_url` |
| `--hyperfleet-api-version` | `clients.hyperfleet_api.version` |
| `--hyperfleet-api-timeout` | `clients.hyperfleet_api.timeout` |
| `--hyperfleet-api-page-size` | `clients.hyperfleet_api.page_size` |
| `--broker-topic` | `clients.broker.topic` |
| `--resource-type` | `resource_type` |
| `--poll-interval` | `poll_interval` |
| `--health-server-bindaddress` | Health/readiness probes bind address (default `:8080`) |
| `--metrics-server-bindaddress` | Prometheus metrics bind address (default `:9090`) |

## Environment Variables

All overrides use the `HYPERFLEET_` prefix unless noted.

| Variable | Maps to YAML field |
|----------|--------------------|
| `HYPERFLEET_DEBUG_CONFIG` | `debug_config` |
| `HYPERFLEET_TRACING_ENABLED` | `tracing_enabled` |
| `HYPERFLEET_SENTINEL_NAME` | `sentinel.name` |
| `HYPERFLEET_LOG_LEVEL` | `log.level` |
| `HYPERFLEET_LOG_FORMAT` | `log.format` |
| `HYPERFLEET_LOG_OUTPUT` | `log.output` |
| `HYPERFLEET_API_BASE_URL` | `clients.hyperfleet_api.base_url` |
| `HYPERFLEET_API_VERSION` | `clients.hyperfleet_api.version` |
| `HYPERFLEET_API_TIMEOUT` | `clients.hyperfleet_api.timeout` |
| `HYPERFLEET_API_PAGE_SIZE` | `clients.hyperfleet_api.page_size` |
| `HYPERFLEET_BROKER_TOPIC` | `clients.broker.topic` |
| `HYPERFLEET_RESOURCE_TYPE` | `resource_type` |
| `HYPERFLEET_POLL_INTERVAL` | `poll_interval` |

## Configuration Validation

Sentinel validates configuration at startup and fails fast on errors:

- **Required fields present**: `resource_type`, `clients.hyperfleet_api.base_url`
- **Non-empty string**: `resource_type` must be a valid entity type plural (e.g. `clusters`, `nodepools`, `wifconfigs`)
- **Valid durations**: All interval fields must be positive
- **Valid CEL expressions**: All `message_data` and `message_decision` expressions must compile
- **API connectivity**: HyperFleet API must be reachable at startup

Use `sentinel config-dump --config config.yaml` to inspect the merged configuration and debug precedence issues.

## Examples

### Minimal Configuration

```yaml
resource_type: clusters
clients:
  hyperfleet_api:
    base_url: http://localhost:8000
```

### Production Configuration with Sharding

```yaml
resource_type: clusters
poll_interval: 5s

message_decision:
  params:
    - name: ref_time
      expr: 'condition("Reconciled").last_updated_time'
    - name: is_reconciled
      expr: 'condition("Reconciled").status == "True"'
    - name: has_ref_time
      expr: 'ref_time != ""'
    - name: is_new_resource
      expr: '!is_reconciled && resource.generation == 1'
    - name: generation_mismatch
      expr: 'resource.generation > condition("Reconciled").observed_generation'
    - name: reconciled_and_stale
      expr: 'is_reconciled && has_ref_time && now - timestamp(ref_time) > duration("30m")'
    - name: not_reconciled_and_debounced
      expr: '!is_reconciled && has_ref_time && now - timestamp(ref_time) > duration("10s")'
  result: "is_new_resource || generation_mismatch || reconciled_and_stale || not_reconciled_and_debounced"

resource_selector:
  - label: shard
    value: "1"

clients:
  hyperfleet_api:
    base_url: http://hyperfleet-api.hyperfleet-system.svc.cluster.local:8080
    timeout: 30s

message_data:
  id: resource.id
  kind: resource.kind
  href: resource.href
  generation: resource.generation
```

### Override via Environment Variables

```bash
export HYPERFLEET_API_BASE_URL=http://api-staging:8000
export HYPERFLEET_LOG_LEVEL=debug
./bin/sentinel serve --config=config.yaml --poll-interval=2s
```

The final `poll_interval` is `2s` (CLI flag wins), `log.level` is `debug` (env var wins over YAML), and `base_url` is `http://api-staging:8000` (env var wins over YAML).
