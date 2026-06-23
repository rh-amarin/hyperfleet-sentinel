# Sentinel Configuration Reference

This document describes the Sentinel configuration options and how to set them
in three formats: YAML, command-line flags, and environment variables.

Overrides are applied in this order: CLI flags > environment variables > YAML file > defaults.

## Config file location

You must point the sentinel at a config file with:

- CLI: `--config` (or `-c`)
- Env: `HYPERFLEET_CONFIG`
- Default: `/etc/hyperfleet/config.yaml`
- A config file must exist at the resolved path (flag, env var, or default /etc/hyperfleet/config.yaml)

## YAML options (SentinelConfig)

All fields use **snake_case** naming.

```yaml
sentinel:
  name: hyperfleet-sentinel-clusters

debug_config: false
tracing_enabled: false

log:
  level: "info"
  format: "json"
  output: "stdout"

clients:
  hyperfleet_api:
    base_url: "http://hyperfleet-api:8000"
    version: "v1"
    timeout: "10s"
    # authorization:       # optional — omit to send no Authorization header
    #   type: static       # "static" or "kubernetes"
    #   token: ""          # required for type=static; use HYPERFLEET_API_AUTH_TOKEN env var
  broker:
    topic: ""

resource_type: "clusters"
poll_interval: "5s"

resource_selector:
  - label: shard
    value: "1"
  - label: region
    value: us-east-1

message_data:
  id: "resource.id"
  kind: "resource.kind"
```

### Top-level fields

- `sentinel.name` (string, required): Sentinel component name/identifier.
- `debug_config` (bool, optional): Log the merged config after load. Default: `false`.
- `tracing_enabled` (bool, optional): Enable OpenTelemetry distributed tracing. Default: `false`.

### Logging (`log`)

- `log.level` (string, optional): Log level (`debug`, `info`, `warn`, `error`). Default: `info`.
- `log.format` (string, optional): Log format (`text`, `json`). Default: `json`.
- `log.output` (string, optional): Log output destination (`stdout`, `stderr`). Default: `stdout`.

### HyperFleet API client (`clients.hyperfleet_api`)

- `base_url` (string, required): Base URL for HyperFleet API requests.
- `version` (string, optional): API version. Default: `v1`.
- `timeout` (duration string, optional): HTTP client timeout. Default: `10s`.
- `authorization` (object, optional): Bearer token configuration for outbound API requests. See [Authorization](#authorization-clientshyperfleet_apiauthorization).

#### Authorization (`clients.hyperfleet_api.authorization`)

When set, the sentinel injects `Authorization: Bearer <token>` on every request to the HyperFleet API. Omit the block entirely to send no authorization header (default).

- `type` (string, required): Token source. One of `static` or `kubernetes`.
- `token` (string, required when `type=static`): The literal bearer token. Set via the `HYPERFLEET_API_AUTH_TOKEN` env var to avoid storing secrets in the config file.

**`type: static`** — uses the literal `token` value:

```yaml
clients:
  hyperfleet_api:
    base_url: http://hyperfleet-api:8000
    authorization:
      type: static
      token: "my-api-token"   # or set via HYPERFLEET_API_AUTH_TOKEN
```

**`type: kubernetes`** — reads the pod's projected ServiceAccount token from `/var/run/secrets/kubernetes.io/serviceaccount/token`. Kubernetes mounts this automatically; no extra fields required:

```yaml
clients:
  hyperfleet_api:
    base_url: http://hyperfleet-api:8000
    authorization:
      type: kubernetes
```

### Broker (`clients.broker`)

- `topic` (string, optional): Broker topic for publishing events.

Note: Broker implementation details (RabbitMQ URL, GCP project ID, etc.) are configured
separately via `broker.yaml` or the hyperfleet-broker library environment variables.

### Sentinel-specific

- `resource_type` (string, required): Resource type to watch (`clusters`, `nodepools`).
- `poll_interval` (duration string, required): How often to poll the API. Default: `5s`.
- `resource_selector` (list, optional): Label selectors to filter resources. Empty means watch all.
- `message_data` (map, required): CEL expressions defining the CloudEvent payload structure.

## Command-line parameters

The following CLI flags override YAML values:

**General**

- `--debug-config` -> `debug_config`
- `--tracing-enabled` -> `tracing_enabled`
- `--name` -> `sentinel.name`
- `--log-level` -> `log.level`
- `--log-format` -> `log.format`
- `--log-output` -> `log.output`

**HyperFleet API**

- `--hyperfleet-api-base-url` -> `clients.hyperfleet_api.base_url`
- `--hyperfleet-api-version` -> `clients.hyperfleet_api.version`
- `--hyperfleet-api-timeout` -> `clients.hyperfleet_api.timeout`

**Broker**

- `--broker-topic` -> `clients.broker.topic`

**Sentinel**

- `--resource-type` -> `resource_type`
- `--poll-interval` -> `poll_interval`

## Environment variables

All deployment overrides use the `HYPERFLEET_` prefix unless noted.

**General**

- `HYPERFLEET_DEBUG_CONFIG` -> `debug_config`
- `HYPERFLEET_TRACING_ENABLED` -> `tracing_enabled`
- `HYPERFLEET_SENTINEL_NAME` -> `sentinel.name`
- `HYPERFLEET_LOG_LEVEL` -> `log.level`
- `HYPERFLEET_LOG_FORMAT` -> `log.format`
- `HYPERFLEET_LOG_OUTPUT` -> `log.output`

**HyperFleet API**

- `HYPERFLEET_API_BASE_URL` -> `clients.hyperfleet_api.base_url`
- `HYPERFLEET_API_VERSION` -> `clients.hyperfleet_api.version`
- `HYPERFLEET_API_TIMEOUT` -> `clients.hyperfleet_api.timeout`
- `HYPERFLEET_API_AUTH_TYPE` -> `clients.hyperfleet_api.authorization.type`
- `HYPERFLEET_API_AUTH_TOKEN` -> `clients.hyperfleet_api.authorization.token`

**Broker**

- `HYPERFLEET_BROKER_TOPIC` -> `clients.broker.topic`

**Sentinel**

- `HYPERFLEET_RESOURCE_TYPE` -> `resource_type`
- `HYPERFLEET_POLL_INTERVAL` -> `poll_interval`

## Examples

### Override API endpoint via environment variable

```bash
export HYPERFLEET_API_BASE_URL=http://localhost:8080
./bin/sentinel serve --config=config.yaml
```

### Override log level via CLI flag

```bash
./bin/sentinel serve --config=config.yaml --log-level=debug
```

### Override multiple settings

```bash
export HYPERFLEET_API_BASE_URL=http://api-staging:8000
export HYPERFLEET_LOG_LEVEL=debug
export HYPERFLEET_LOG_FORMAT=json
./bin/sentinel serve --config=config.yaml --poll-interval=2s
```

### Precedence example

Given this config file:

```yaml
log:
  level: "info"
```

And these overrides:

```bash
export HYPERFLEET_LOG_LEVEL=warn
./bin/sentinel serve --config=config.yaml --log-level=debug
```

The final log level will be `debug` (CLI flag wins over env var and config file).
