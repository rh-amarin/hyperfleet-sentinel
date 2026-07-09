# HyperFleet Sentinel Operator Guide

> **Audience:** Operators deploying and configuring Sentinel service.

This comprehensive guide teaches operators how to deploy, configure, and operate the HyperFleet Sentinel service—a polling-based event publisher that drives cluster lifecycle orchestration.

## Table of Contents

1. [Introduction](#1-introduction)
   - [What is Sentinel?](#11-what-is-sentinel)
   - [When to Use Sentinel](#12-when-to-use-sentinel)
2. [Core Concepts](#2-core-concepts)
   - [Decision Engine](#21-decision-engine)
      - [How the Engine Works](#211-how-the-engine-works)
      - [Default Configuration](#212-default-configuration)
      - [Complete Decision Flow](#213-complete-decision-flow)
   - [Resource Filtering](#22-resource-filtering)
3. [Configuration Reference](#3-configuration-reference)
   - [Configuration File Structure](#31-configuration-file-structure)
   - [Required Fields](#32-required-fields)
   - [Optional Fields](#33-optional-fields)
   - [Resource Selector](#34-resource-selector)
   - [Message Data (CEL Expressions)](#35-message-data-cel-expressions)
   - [Broker Configuration](#36-broker-configuration)
4. [Deployment Checklist](#4-deployment-checklist)
5. [Additional Resources](#additional-resources)

**Appendices:**

- [Appendix A: Troubleshooting](#appendix-a-troubleshooting)

---

## 1. Introduction

### 1.1 What is Sentinel?

**Sentinel is the component within the HyperFleet system responsible for triggering reconciliation events for changes in managed API resources.** It acts as the centralized trigger mechanism for the orchestration platform.

**Key Benefits:**

- **Single source of truth** - Centralized decision logic for when reconciliation should occur
- **Configurable polling strategy** - Different reconciliation rates for stable vs transitional resources
- **Broker abstraction** - Supports multiple message broker backends
- **Horizontal scalability** - Multiple instances can distribute workload through resource filtering

**Core Responsibilities:**

1. **Poll HyperFleet API** for resource updates at configurable intervals
2. **Evaluate resources** using a decision engine to determine when events should be published
3. **Publish CloudEvents** to a message broker
4. **Track metrics** for observability and debugging

**Architecture Overview:**

```mermaid
graph LR
    A[Sentinel] -->|Poll| B[HyperFleet API]
    B -->|Resources| A
    A -->|Evaluate| C[Decision Engine]
    C -->|Publish Events| D[Message Broker]
    D -->|Consume Events| E[Adapters]
    E -->|Update Status| B
```

Sentinel publishes events to a message broker, which fans out messages to downstream adapters. It uses a **fully configurable CEL-based decision engine** (`message_decision`) that determines when to publish. The default configuration covers four triggers:

- **New resource**: Publish immediately for resources that have never been processed (generation 1, not reconciled)
- **Generation mismatch**: Publish immediately when the resource spec has changed but not yet been fully processed
- **Reconciled and stale**: Publish periodically for stable resources to ensure eventual consistency
- **Not reconciled, debounced**: Publish more frequently for transitional resources, with a debounce to prevent event storms

### 1.2 When to Use Sentinel

Deploy Sentinel when you need:

- **Event-driven orchestration** for cluster lifecycle management
- **Centralized reconciliation logic** instead of distributed polling by each adapter
- **Configurable polling intervals** with different rates for reconciled vs not-reconciled resources
- **Horizontal scaling** through resource filtering
- **Broker abstraction** to support multiple message broker backends

---

## 2. Core Concepts

### 2.1 Decision Engine

Sentinel's decision engine is **fully CEL-based**: all publish/skip logic is expressed as named CEL parameters and a boolean result expression configured in `message_decision`. There is no hardcoded Go-level decision logic — the entire strategy is defined in configuration.

Each poll cycle, the engine:
1. Evaluates named `params` in authored order, accumulating intermediate values
2. Evaluates the `result` expression using all param values
3. Publishes an event if `result` is `true`; otherwise skips

**How Sentinel Reads Resource State:**

When Sentinel polls the HyperFleet API, it retrieves cluster or nodepool resources with their current state. Two CEL variables are always available during evaluation:

- **`resource`** — the API resource as a map (`id`, `kind`, `href`, `generation`, `created_time`, `updated_time`, `labels`, `owner_references`, `metadata`)
- **`now`** — the current evaluation timestamp (`timestamp` type)

The **`condition(name)`** CEL function looks up a status condition by type name (e.g., `condition("Reconciled")`). Each condition exposes: `status`, `observed_generation`, `last_updated_time`, `last_transition_time`, `reason`, `message`. If the condition is absent, all fields are zero values (empty strings, `0` for `observed_generation`), so CEL expressions can guard safely with `ref_time != ""`.

#### 2.1.1 How the Engine Works

`message_decision` has two parts:

- **`params`** — a list of named CEL expressions evaluated in order. Each param can reference `resource`, `now`, `condition()`, and any previously defined param. Params are declared in dependency order: if `B` uses `A`, `A` must appear first.
- **`result`** — a boolean CEL expression evaluated after all params, combining them into the final publish decision.

**Outcomes:**

| Result | `ShouldPublish` | Reason logged |
|--------|-----------------|---------------|
| `result` is `true` | `true` | `"message decision matched"` |
| `result` is `false` | `false` | `"message decision result is false"` |

If any param or the result expression fails to evaluate (type error, missing variable, etc.) the resource is skipped and the error is logged with the failing param name.

#### 2.1.2 Default Configuration

When `message_decision` is omitted from the config file, Sentinel uses the following default. It implements four triggers:

| Param | CEL Expression | Trigger |
|-------|----------------|---------|
| `ref_time` | `condition("Reconciled").last_updated_time` | Reference timestamp from Reconciled condition |
| `is_reconciled` | `condition("Reconciled").status == "True"` | Resource reconciled status |
| `has_ref_time` | `ref_time != ""` | Guard: Reconciled condition exists |
| `is_new_resource` | `resource.generation == 1 && !has_ref_time` | New resource: no Reconciled condition yet |
| `generation_mismatch` | `resource.generation > condition("Reconciled").observed_generation` | Spec changed but not yet processed |
| `reconciled_and_stale` | `is_reconciled && has_ref_time && now - timestamp(ref_time) > duration("30m")` | Stable resource drifting past 30 min |
| `not_reconciled_and_debounced` | `!is_reconciled && has_ref_time && now - timestamp(ref_time) > duration("10s")` | Transitional resource debounced past 10 s |

**Result:** `is_new_resource || generation_mismatch || reconciled_and_stale || not_reconciled_and_debounced`

**What each trigger covers:**

- **`is_new_resource`** — Catches brand-new resources with `generation == 1` that have no Reconciled condition yet (adapter has never seen them). Publishes within one poll interval of creation. Once the adapter creates the Reconciled condition, `not_reconciled_and_debounced` takes over with its 10 s debounce.
- **`generation_mismatch`** — Catches spec changes immediately: the HyperFleet API increments `generation` on every spec update; adapters increment `observed_generation` on the Reconciled condition as they process it. A gap means unprocessed changes.
- **`reconciled_and_stale`** — Ensures eventual consistency on stable resources by re-publishing periodically even when the spec is in sync, handling external drift and transient failures.
- **`not_reconciled_and_debounced`** — Drives faster re-publishing for transitional resources, while the debounce prevents event storms.

> **Customization:** Override any threshold by providing your own `message_decision` in the config file. See the dev example (`configs/dev-example.yaml`) which uses `2m`/`5s` instead of `30m`/`10s` for faster local iteration.

#### 2.1.3 Complete Decision Flow

```mermaid
graph TD
    START[Poll Resource] --> EVAL[Evaluate CEL params in order]
    EVAL --> RESULT{result expression}
    RESULT -->|true| PUB[Publish event<br/>reason: message decision matched]
    RESULT -->|false| SKIP[Skip<br/>reason: message decision result is false]
    RESULT -->|eval error| ERR[Skip<br/>reason: param/result evaluation failed]
```

With the **default config**, the result expression is:
`is_new_resource || generation_mismatch || reconciled_and_stale || not_reconciled_and_debounced`

The params feed into it in this logical order:

1. `ref_time`, `is_reconciled`, `has_ref_time` — extract condition state
2. `is_new_resource`, `generation_mismatch` — immediate triggers (no time check needed)
3. `reconciled_and_stale`, `not_reconciled_and_debounced` — time-based triggers (guarded by `has_ref_time`)

**Key Takeaways:**

- **All logic is configurable** — the engine itself has no hardcoded triggers
- **Params are short-circuit safe** — `has_ref_time` guards time expressions against missing conditions
- **Single outcome** — regardless of which param fired, the reason is always `"message decision matched"`

> **Scalability note:** The CEL decision engine evaluates all resources matching the label selector in-memory each poll cycle. At large scale (thousands of resources), use `resource_selector` to shard resources across multiple Sentinel instances. A future `server_filters` config field is planned to allow server-side pre-filtering before CEL evaluation.

### 2.2 Resource Filtering

Resource filtering enables **horizontal scaling** by allowing operators to distribute resources across multiple Sentinel instances using label-based selectors.

**How It Works:**

The `resource_selector` field defines label key-value pairs that filter which resources a Sentinel instance watches:

```yaml
resource_selector:
  - label: region
    value: us-east-1
  - label: environment
    value: production
```

**Selection Logic:**

- **Empty selector** (`[]`): Watch ALL resources (default behavior)
- **Single selector**: Match resources with that label
- **Multiple selectors**: Match resources with ALL labels (AND logic)

**Common Filtering Patterns:**

| Pattern | Example                                     | Use Case |
|----------|---------------------------------------------|----------|
| **Regional** | `region: us-east`, `region: eu-west`        | Geographic distribution |
| **Environment** | `environment: prod`, `environment: staging` | Isolation by environment |
| **Index-based** | `index: "1"`, `index: "2"`, `index: "3"`    | Numeric distribution for high volume |
| **Combined** | `region: us-east` + `environment: prod`     | Multi-dimensional filtering |

**Architecture Diagram:**

```mermaid
graph TB
    API[HyperFleet API<br/>All Resources]

    API --> S1[Sentinel US-East Clusters<br/>resource_type: clusters<br/>selector: region=us-east]
    API --> S2[Sentinel US-West Clusters<br/>resource_type: clusters<br/>selector: region=us-west]
    API --> S3[Sentinel EU-Central NodePools<br/>resource_type: nodepools<br/>selector: region=eu-central]

    S1 --> B1[Broker Topic:<br/>us-east-clusters]
    S2 --> B2[Broker Topic:<br/>us-west-clusters]
    S3 --> B3[Broker Topic:<br/>eu-central-nodepools]

    B1 --> A1a[Adapter US-East-1]
    B1 --> A1b[Adapter US-East-2]
    B2 --> A2a[Adapter US-West-1]
    B2 --> A2b[Adapter US-West-2]
    B3 --> A3[Adapter EU-Central]
```

**Important Considerations:**

1. **No Coordination**: Sentinel instances operate independently with no coordination
2. **Coverage Responsibility**: Operators must ensure all resources are covered by selectors
3. **Gaps Dangerous**: Resources not matching any selector will never reconcile

> **Important**: Multiple instances watching the same resource **will** produce duplicate events. Always use non-overlapping `resource_selector` values across instances. Do not increase `replicaCount` to scale — deploy separate instances with distinct selectors instead. See the [Known Limitations](multi-instance-deployment.md#known-limitations) section for details.

**Broker Topic Isolation:**

When using multiple filtered instances, consider using separate broker topics to enable independent processing:

```yaml
# US-East instance
broker:
  topic: hyperfleet-us-east-clusters

# US-West instance
broker:
  topic: hyperfleet-us-west-clusters
```

#### Multi-Region Configuration

For multi-region deployment examples using `resource_selector`, see [Resource Selector
Strategies](multi-instance-deployment.md#resource-filtering-strategies).

For detailed deployment examples, see [docs/multi-instance-deployment.md](multi-instance-deployment.md).

---

## 3. Configuration Reference

### 3.1 Configuration File Structure

Sentinel uses YAML-based configuration with environment variable overrides for sensitive data. Configuration is loaded from a file specified via the `--config` flag.

**Basic Structure:**

```yaml
# Required: Resource type to watch
resource_type: clusters

# Optional: Polling interval
poll_interval: 5s

# Optional: Resource filtering
resource_selector:
  - label: region
    value: us-east-1

# Required: HyperFleet API configuration
hyperfleet_api:
  endpoint: http://hyperfleet-api.hyperfleet-system.svc.cluster.local:8000
  timeout: 5s

# Optional: CloudEvent payload definition
message_data:
  id: "resource.id"
  kind: "resource.kind"
  href: "resource.href"
  generation: "resource.generation"
```

**Configuration Precedence:**

1. Environment variables (highest)
2. Configuration file
3. Built-in defaults (lowest)

### 3.2 Required Fields

These fields MUST be present in the configuration file or Sentinel will fail to start:

| Field | Type | Description | Example                                                          |
|-------|------|-------------|------------------------------------------------------------------|
| `resource_type` | string | Resource to watch (`clusters` or `nodepools`) | `clusters`                                                       |
| `hyperfleet_api.endpoint` | string | HyperFleet API base URL | `http://hyperfleet-api.hyperfleet-system.svc.cluster.local:8000` |

### 3.3 Optional Fields

These fields have sensible defaults and can be omitted:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `poll_interval` | duration | `5s` | How often to poll the API for resource updates |
| `message_decision` | object | See defaults | CEL-based decision logic (params + result expression) |
| `hyperfleet_api.timeout` | duration | `5s` | Request timeout for API calls |
| `resource_selector` | array | `[]` | Label selectors for filtering (empty = all resources) |
| `message_data` | map | `{}` | CEL expressions for CloudEvents payload |
| `topic` | string | `""` | Override broker topic name (defaults to Helm template) |

### 3.4 Resource Selector

The `resource_selector` field enables horizontal scaling by filtering resources based on labels.

**Structure:**

```yaml
resource_selector:
  - label: <label-key>
    value: <label-value>
  - label: <another-key>
    value: <another-value>
```

**Behavior:**

- **Empty** (`[]` or omitted): Watch ALL resources
- **Single selector**: Match resources with that specific label
- **Multiple selectors**: Match resources with ALL labels (AND logic)

### 3.5 Message Data (CEL Expressions)

The `message_data` field defines the CloudEvents payload structure using **Common Expression Language (CEL)** expressions.

**How It Works:**

1. Each key-value pair in `message_data` becomes a field in the CloudEvent `data` payload
2. Values are CEL expressions evaluated with access to two variables:
   - `resource` - The HyperFleet resource object (cluster or nodepool)
   - `reason` - The decision reason string (`"message decision matched"` when publishing)
3. Nested maps create nested objects in the payload

**Available CEL Variables:**

| Variable | Type | Description | Example Fields |
|----------|------|-------------|----------------|
| `resource` | Resource | The HyperFleet resource | `id`, `kind`, `href`, `generation`, `labels`, `created_time`, `updated_time`, `owner_references`, `metadata` |
| `reason` | string | Decision engine reason | `"message decision matched"`, `"message decision result is false"` |

**CEL Expression Syntax:**

```yaml
message_data:
  # Field access
  id: "resource.id"

  # Nested field access
  cluster_id: "resource.owner_references.id"

  # Conditionals (ternary operator) — use condition() function to access status conditions
  reconciled_status: 'condition("Reconciled").status == "True" ? "Reconciled" : "NotReconciled"'

  # String literals (must use quotes inside CEL expression)
  source: '"hyperfleet-sentinel"'

  # Numeric/boolean literals
  generation: "resource.generation"

  # Nested objects
  owner_references: "resource.owner_references"
```

**Cluster Pattern:**

```yaml
message_data:
  id: "resource.id"
  kind: "resource.kind"
  href: "resource.href"
  generation: "resource.generation"
```

**NodePool Pattern with Owner References:**

```yaml
message_data:
  id: "resource.id"
  kind: "resource.kind"
  href: "resource.href"
  generation: "resource.generation"
  owner_references: "resource.owner_references"
```

**Validation:**

- All leaf values MUST be non-empty CEL expression strings
- Empty values or `nil` will cause configuration validation failure:

  ```text
  Error: invalid config: message_data.id: empty CEL expression is not allowed
  ```

**CloudEvents Output:**

The `message_data` configuration produces CloudEvents with the following structure:

```json
{
  "specversion": "1.0",
  "type": "com.redhat.hyperfleet.cluster.reconcile",
  "source": "hyperfleet-sentinel",
  "id": "uuid-generated",
  "time": "2025-01-01T10:00:00Z",
  "datacontenttype": "application/json",
  "data": {
    // Your message_data CEL expressions evaluated here
    "id": "cluster-abc123",
    "kind": "Cluster",
    "href": "/api/v1/clusters/cluster-abc123",
    "generation": 5
  }
}
```

### 3.6 Broker Configuration

Broker configuration is managed by the [hyperfleet-broker library](https://github.com/openshift-hyperfleet/hyperfleet-broker). Configuration is split between:

1. **broker.yaml** - Non-sensitive broker settings (type, project ID, etc.)
2. **Environment variables** - Sensitive credentials and connection strings

**Configuration File: broker.yaml**

```yaml
broker:
  type: rabbitmq  # or googlepubsub

  # RabbitMQ-specific settings
  rabbitmq:
    exchange_type: topic
    # URL should be set via BROKER_RABBITMQ_URL env var

  # Google Pub/Sub-specific settings
  googlepubsub:
    project_id: my-gcp-project
    # Credentials via GOOGLE_APPLICATION_CREDENTIALS or ADC
```

**Environment Variables:**

| Variable | Broker | Description | Example |
|----------|--------|-------------|---------|
| `BROKER_RABBITMQ_URL` | RabbitMQ | Complete connection URL with credentials | `amqp://user:pass@localhost:5672/vhost` |
| `BROKER_GOOGLEPUBSUB_PROJECT_ID` | Pub/Sub | GCP project ID | `my-gcp-project` |
| `GOOGLE_APPLICATION_CREDENTIALS` | Pub/Sub | Service account key path (local dev/testing only, production GKE uses Workload Identity) | `/path/to/key.json` |
| `HYPERFLEET_BROKER_TOPIC` | Both | Topic name for publishing events | `hyperfleet-prod-clusters` |
| `BROKER_CONFIG_FILE` | Both | Path to broker config file | `/app/broker.yaml` |
| `PUBSUB_EMULATOR_HOST` | Pub/Sub | Pub/Sub emulator endpoint (local dev/testing only) | `localhost:8085` |

**Topic Naming:**

The `HYPERFLEET_BROKER_TOPIC` environment variable sets the topic name where events are published. You can use any naming convention that fits your deployment requirements.

Example topic names:

- `hyperfleet-prod-clusters`
- `us-east-clusters`

When using the provided Helm chart, the default template uses `{namespace}-{resource_type}` (e.g., `hyperfleet-dev-clusters`), but this can be overridden by setting the `HYPERFLEET_BROKER_TOPIC` environment variable or the `broker.topic` Helm value.

**Broker Type: RabbitMQ**

```yaml
# broker.yaml
broker:
  type: rabbitmq
  rabbitmq:
    exchange_type: topic
```

```bash
# Environment variables
export BROKER_RABBITMQ_URL="amqp://guest:guest@localhost:5672/"
export HYPERFLEET_BROKER_TOPIC="hyperfleet-dev-clusters"
```

**Broker Type: Google Pub/Sub**

```yaml
# broker.yaml
broker:
  type: googlepubsub
  googlepubsub:
    project_id: hcm-hyperfleet
```

**Local Development Authentication:**

For local development and testing, use one of these authentication methods:

```bash
# Set the topic name
export HYPERFLEET_BROKER_TOPIC="hyperfleet-dev-clusters"

# Option 1: Use personal Application Default Credentials (local development)
gcloud auth application-default login

# Option 2: Use service account key file (local development/testing)
export GOOGLE_APPLICATION_CREDENTIALS="/path/to/service-account-key.json"
```

**Production GKE Authentication:**

When deploying to production GKE, use **Workload Identity Federation** for authentication. This method does not require `GOOGLE_APPLICATION_CREDENTIALS` or `gcloud auth` commands - credentials are automatically provided to the pod:

```bash
# Only the topic name is required via environment variable
export HYPERFLEET_BROKER_TOPIC="hyperfleet-prod-clusters"

# No GOOGLE_APPLICATION_CREDENTIALS needed - Workload Identity handles authentication
```

For Workload Identity setup instructions, see [Configure Workload Identity](deployment.md#workload-identity-for-pubsub).

**Broker Configuration Reference:**

For complete broker configuration options, see the [hyperfleet-broker documentation](https://github.com/openshift-hyperfleet/hyperfleet-broker).

---

## 4. Deployment Checklist

Follow this checklist to ensure successful Sentinel deployment and operation.

### Phase 1: Configuration Planning

**Define Resource Monitoring Scope**

- [ ] Determine `resource_type` to monitor: `clusters` or `nodepools`
- [ ] Define `resource_selector` labels for filtering resources
  - Leave empty (`[]`) to monitor all resources
  - Use label selectors for horizontal scaling
  - Reference: [Resource Filtering](#22-resource-filtering)

**Configure Reconciliation Parameters**

- [ ] Review and adjust polling and decision parameters:
  - `poll_interval` - How often to poll the HyperFleet API (default: `5s`)
  - `message_decision` - CEL-based decision logic with configurable thresholds
  - Reference: [Optional Fields](#optional-fields)

**Design CloudEvents Payload**

- [ ] Define `message_data` CEL expressions for event payload structure
- [ ] Reference: [Message Data (CEL Expressions)](#message-data-cel-expressions)

**Configure HyperFleet API Connection**

- [ ] **Ensure HyperFleet API is deployed and accessible**
  - **Critical:** Sentinel performs connectivity verification at startup and **will fail to start if the API is unavailable**
  - **Rolling updates will not succeed** until API connectivity is restored
- [ ] Set `hyperfleet_api.endpoint` to HyperFleet API URL
- [ ] Adjust `hyperfleet_api.timeout` if needed (default: `5s`)
- [ ] Reference: [Required Fields](#required-fields)

### Phase 2: Broker Preparation

**Select and Configure Broker**

- [ ] Choose broker type: `rabbitmq` or `googlepubsub`
- [ ] Reference: [Broker Configuration](#broker-configuration)

**Provision Broker Infrastructure**

- [ ] **RabbitMQ:**
  - Create exchange and queues
  - Configure topic routing keys
  - Set retention and delivery policies
- [ ] **Google Pub/Sub:**
  - Create topic with your chosen naming convention (e.g., `hyperfleet-prod-clusters`)
  - Configure message retention duration
  - Set up dead-letter topic (optional)

**Configure Authentication and Permissions**

- [ ] **RabbitMQ:**
  - Create credentials for Sentinel service
  - Grant publish permissions to exchange
  - Prepare `BROKER_RABBITMQ_URL` connection string
- [ ] **Google Pub/Sub:**
  - Configure Workload Identity Federation (GKE) or service account
  - Grant `roles/pubsub.publisher` role to Sentinel service account

### Phase 3: Deployment

**Deploy with Helm**

- [ ] Update Helm chart `values.yaml` with:
  - Sentinel configuration (`config` section)
  - Broker configuration (`broker` section)
  - Sensitive credentials in `secrets` or reference to existing secrets
- [ ] Install Sentinel using Helm chart:

  ```bash
  helm install sentinel oci://quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel-chart \
    --namespace hyperfleet-system \
    --values values.yaml
  ```

- [ ] Verify deployment:

  ```bash
  kubectl get deployment -n hyperfleet-system sentinel
  kubectl get pods -n hyperfleet-system -l app.kubernetes.io/name=sentinel
  ```

- [ ] Reference: [Helm Chart README](../charts/README.md)

### Phase 4: Post-Deployment Validation

**Verify Service Health**

- [ ] Check health endpoint: `curl http://<sentinel-service>:8080/healthz`
- [ ] Check readiness endpoint: `curl http://<sentinel-service>:8080/readyz`
  - **Note:** The `/readyz` endpoint returns `false` until the first successful poll completes and broker health checks pass. Pods intentionally stay unready during initial startup.
  - If startup latency causes false-positive readiness probe failures, tune the Kubernetes readiness probe timing (e.g., increase `initialDelaySeconds` or `periodSeconds`) in your Helm values.
- [ ] Review pod logs for startup errors:

  ```bash
  kubectl logs -n hyperfleet-system -l app.kubernetes.io/name=sentinel
  ```

- [ ] Verify Sentinel is publishing events:

  ```bash
  kubectl logs -n hyperfleet-system -l app.kubernetes.io/name=sentinel | grep -E "Fetched resources|Trigger cycle completed"
  ```

  Expected log output when Sentinel is operating correctly:

  ```text
  Fetched resources count=15 label_selectors=1 topic=hyperfleet-dev-clusters subset=clusters
  Trigger cycle completed total=15 published=3 skipped=12 duration=0.125s topic=hyperfleet-dev-clusters subset=clusters
  ```

  - `count` - Number of resources fetched from the API matching the resource selector
  - `published` - Number of events published (`message_decision` result was `true`)
  - `skipped` - Number of resources skipped (no reconciliation needed)

For detailed deployment guidance, see [docs/deployment.md](deployment.md)

---

## Additional Resources

### Documentation

- **[Running Sentinel](sentinel-for-gke-dev.md)** - Detailed guide for GKE deployments
- **[Metrics Documentation](metrics.md)** - Complete metrics catalog with PromQL examples
- **[Multi-Instance Deployment](multi-instance-deployment.md)** - Horizontal scaling strategies
- **[Testcontainers Documentation](testcontainers.md)** - Integration testing with testcontainers
- **[Helm Chart README](../charts/README.md)** - Helm chart configuration reference

### Tools and Libraries

- **[Common Expression Language (CEL)](https://github.com/google/cel-spec)** - CEL specification for `message_data`
- **[CloudEvents](https://cloudevents.io/)** - CloudEvents specification

---

## Appendix A: Troubleshooting

| Symptom                                                                                                                      | Likely Cause | Solution                                                                                                                                                                                                                                                                                                                 |
|------------------------------------------------------------------------------------------------------------------------------|--------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Events not published, resources not found** | Resource selector mismatch | Verify `resource_selector` matches resource labels. Empty selector watches ALL resources. Check logs: `kubectl logs -n hyperfleet-system -l app.kubernetes.io/name=sentinel`                                                                                                                                             |
| **Events not published, resources found but skipped**                                                                           | Decision result is false | Normal behavior (reason: `"message decision result is false"`). Events publish when the `message_decision` result expression evaluates to `true`. With the default config: new resource (`generation==1 && !reconciled`), generation mismatch, reconciled and stale >30m, or not reconciled and debounced >10s.                                          |
| **API connection errors, DNS lookup fails**                                                                                  | Wrong service name or namespace | Verify endpoint format: `http://<service>.<namespace>.svc.cluster.local:8000`. Check API is running: `kubectl get pods -n hyperfleet-system -l app=hyperfleet-api`                                                                                                                                                       |
| **API returns 401 Unauthorized**                                                                                             | Missing authentication | Add auth headers to `hyperfleet_api` config if API requires authentication.                                                                                                                                                                                                                                              |
| **API returns 404 Not Found**                                                                                                | Wrong API version in path | Verify endpoint uses correct API version: `/api/v1/clusters` or `/api/hyperfleet/v1/clusters`                                                                                                                                                                                                                            |
| **Broker PermissionDenied (Pub/Sub)**                                                                                        | Missing publisher role | Grant role: `gcloud projects add-iam-policy-binding ${GCP_PROJECT} --role="roles/pubsub.publisher" --member="principal://iam.googleapis.com/..."`                                                                                                                                                                        |
| **Broker Topic not found (Pub/Sub)**                                                                                         | Topic doesn't exist | Create topic: `gcloud pubsub topics create hyperfleet-prod-clusters --project=${GCP_PROJECT}`                                                                                                                                                                                                                            |
| **Broker type mismatch**                                                                                                     | Config doesn't match actual broker | Ensure `broker.type` matches: `rabbitmq` or `googlepubsub`. Check: `kubectl get configmap sentinel -o jsonpath='{.data.broker\.yaml}'`                                                                                                                                                                                   |
| **High CPU/memory usage**                                                                                                    | Too many resources or slow API | Check `kubectl top pod -n hyperfleet-system -l app=sentinel`. Consider horizontal scaling with `resource_selector` or increase poll intervals.                                                                                                                                                                           |
| **Error: resource_type is required**                                                                                         | Missing required config field | Add `resource_type: clusters` or `resource_type: nodepools` to configuration.                                                                                                                                                                                                                                            |
| **Error: invalid resource_type**                                                                                             | Invalid value | Use only `clusters` or `nodepools`.                                                                                                                                                                                                                                                                                      |
| **Error: hyperfleet_api.endpoint is required**                                                                               | Missing required config field | Add `hyperfleet_api.endpoint: http://hyperfleet-api.hyperfleet-system.svc.cluster.local:8000`                                                                                                                                                                                                                            |
| **Error: poll_interval must be positive**                                                                                    | Zero or negative interval | Set `poll_interval: 5s` (must be > 0).                                                                                                                                                                                                                                                                                   |
| **Error: OpenAPI client not generated**                                                                                      | Missing generated code | Run `make generate && make build` before starting Sentinel.                                                                                                                                                                                                                                                              |
| **Pods stay unready after startup**                                                                                         | Normal startup behavior | The `/readyz` endpoint returns `false` until the first successful poll completes and broker health checks pass. This is expected. If readiness probe failures persist beyond initial startup, check pod logs and broker connectivity. Tune probe timing (e.g., increase `initialDelaySeconds`) in Helm values if needed. |
| **Health/readiness endpoints return errors**                                                                                 | Configuration validation failed | Check pod logs for startup errors: `kubectl logs -n hyperfleet-system -l app.kubernetes.io/name=sentinel`. Verify all required config fields.                                                                                                                                                                            |

---
