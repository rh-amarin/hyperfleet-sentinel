# Deploying Multiple Sentinel Instances

> **Audience:** Operations teams deploying Sentinel at scale.

Sentinel supports horizontal scaling through multiple dimensions: by resource type (separate instances for clusters vs nodepools) and by label-based resource filtering within the same resource type. Deploy multiple Sentinel instances with different `resource_selector` values to distribute the workload.

> **Important**: There is no coordination between Sentinel instances. Operators must ensure selectors are **non-overlapping** (to avoid duplicate events) and that all resources are covered by the combined selectors (to avoid gaps). See [Known Limitations](#known-limitations) for details.

## Using Helm for Multi-Instance Deployment

Deploy multiple Sentinel instances by installing the Helm chart multiple times with different release names and configurations:

```bash
# Instance 1: Watch clusters in us-east region
helm install sentinel-us-east oci://quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel-chart \
  --namespace hyperfleet-system \
  --set config.resourceSelector[0].label=region \
  --set config.resourceSelector[0].value=us-east

# Instance 2: Watch clusters in us-west region
helm install sentinel-us-west oci://quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel-chart \
  --namespace hyperfleet-system \
  --set config.resourceSelector[0].label=region \
  --set config.resourceSelector[0].value=us-west

# Instance 3: Watch clusters in eu-central region
helm install sentinel-eu-central oci://quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel-chart \
  --namespace hyperfleet-system \
  --set config.resourceSelector[0].label=region \
  --set config.resourceSelector[0].value=eu-central
```

## Using Values Files for Complex Configurations

For more complex setups, create separate values files for each instance:

**values-us-east.yaml:**

```yaml
config:
  resourceType: clusters
  resourceSelector:
    - label: region
      value: us-east
    - label: environment
      value: production
```

**values-us-west.yaml:**

```yaml
config:
  resourceType: clusters
  resourceSelector:
    - label: region
      value: us-west
    - label: environment
      value: production
```

Deploy using values files:

```bash
helm install sentinel-us-east oci://quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel-chart \
  --namespace hyperfleet-system \
  -f values-us-east.yaml

helm install sentinel-us-west oci://quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel-chart \
  --namespace hyperfleet-system \
  -f values-us-west.yaml
```

## Resource Filtering Strategies

| Strategy | Description | Example Labels |
|----------|-------------|----------------|
| Regional | Distribute by geographic region | `region: us-east`, `region: eu-west` |
| Environment | Separate by environment type | `environment: production`, `environment: staging` |
| Numeric subsets | Numeric labels for even distribution | `subset: "0"`, `subset: "1"`, `subset: "2"` |
| Cluster Type | Filter by cluster characteristics | `cluster-type: hypershift`, `cluster-type: standalone` |

Multiple labels use AND logic - all labels must match for a resource to be selected.

## Broker Topic Isolation

When deploying multiple Sentinel instances, consider using separate broker topics to avoid event duplication and enable independent processing:

```bash
# Instance for us-east with dedicated topic
helm install sentinel-us-east oci://quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel-chart \
  --namespace hyperfleet-system \
  --set config.resourceSelector[0].label=region \
  --set config.resourceSelector[0].value=us-east \
  --set broker.topic=hyperfleet-clusters-us-east

# Instance for us-west with dedicated topic
helm install sentinel-us-west oci://quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel-chart \
  --namespace hyperfleet-system \
  --set config.resourceSelector[0].label=region \
  --set config.resourceSelector[0].value=us-west \
  --set broker.topic=hyperfleet-clusters-us-west
```

By default, the Helm chart generates topic names using the pattern `{namespace}-{resourceType}`. Override with `broker.topic` when you need per-instance isolation.

## MVP Recommendation

For initial deployments, start with a **single Sentinel instance** watching all resources:

```yaml
config:
  resourceType: clusters
  resourceSelector: []  # Empty = watch all resources
```

Scale to multiple instances as your cluster count grows or when you need regional isolation.

---

## Known Limitations

### No Built-In Deduplication or Leader Election

Sentinel has no inter-instance coordination. Running multiple replicas with the same or overlapping resource selector (`resource_selector` in Sentinel config YAML, `resourceSelector` in Helm values) produces proportionally more duplicate events on the broker. Each replica independently polls the API and publishes events for every matching resource, resulting in:

- Increased load on the API, PostgreSQL, broker, and adapters — without benefit
- Adapters processing the same cluster multiple times per poll cycle
- No deduplication at the Sentinel or broker layer

> **Important**: Do not increase `replicaCount` to scale Sentinel. Multiple replicas with the same selector will duplicate events. Scale by deploying separate Sentinel instances with **non-overlapping** `resource_selector` values instead.

This is an architectural decision documented in ADR-0004 (Sentinel as a Stateless Polling Reconciliation Loop). Sentinel is intentionally stateless with at-least-once delivery semantics — adapters are expected to be idempotent.

### Recommended Deployment Configuration

Deploy **one Sentinel instance per distinct resource partition** (label selector subset). Scale horizontally by adding instances with non-overlapping selectors:

```yaml
# Instance A: watches region=us-east
config:
  resourceType: clusters
  resourceSelector:
    - label: region
      value: us-east

# Instance B: watches region=us-west (no overlap with Instance A)
config:
  resourceType: clusters
  resourceSelector:
    - label: region
      value: us-west
```

Do **not** run multiple replicas of the same Sentinel configuration. If you need high availability for a single partition, rely on Kubernetes restart policies and `PodDisruptionBudget` (see below) rather than replica scaling.

### Future: Automated Partitioning

The current label-based partitioning model is a known MVP limitation. The architecture repo (sentinel.md, Technical Debt section) documents a planned remediation path: automated shard coverage validation or coordinated sharding with a registry. A future Epic will address both automatic partition assignment and gap detection (resources not matched by any Sentinel instance).

---

## PodDisruptionBudget

**What**: Ensures minimum Sentinel availability during cluster maintenance.

**Configuration for Single-Replica Deployments** (typical topology):

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: sentinel-pdb
  namespace: hyperfleet-system
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: sentinel
```

**Operational Impact**:

- **Single replica protection**: `minAvailable: 1` blocks voluntary pod eviction when only 1 replica exists
- **Maintenance blocking**: Node drains will be delayed until Sentinel pods are manually drained or scaled up
- **Multiple Sentinels**: Each Sentinel deployment (per resource selector) can have its own PDB
- **Trade-off**: Maintenance operations may require manual intervention for single-replica Sentinels

> **Note**: Cluster maintenance operations respect Sentinel availability requirements.

---

## Operational Guidance

### Resource Requirements

#### Production Recommendations

```yaml
resources:
  requests:
    cpu: 100m      # Baseline for polling every 5s
    memory: 128Mi  # Baseline for ~1000 resources
  limits:
    cpu: 500m      # Handle traffic spikes
    memory: 512Mi  # Memory for large resource sets
```

> **Note**: Resource requirements will be validated and updated based on actual consumption profiling in HYPERFLEET-556.

#### Scaling Guidelines

**CPU Scaling**:

- **Base load**: 50-100m for basic polling
- **Per 1000 resources**: Additional 50m CPU
- **High churn environments**: Additional 100m for frequent events

**Memory Scaling**:

- **Base load**: 64Mi for service overhead
- **Per 1000 resources**: Additional 32Mi memory
- **Complex resource selectors**: Additional 16Mi per selector rule

**Example Calculation**:

```
5000 resources + complex selectors:
CPU: 100m + (5 × 50m) + 100m = 450m
Memory: 64Mi + (5 × 32Mi) + 16Mi = 240Mi
```

### Scaling Strategy

#### Horizontal Scaling (Label Partitioning)

**Approach**: Deploy multiple Sentinel instances with different `resource_selector` configurations.

**Benefits**:

- Linear performance scaling
- Fault isolation (one failure doesn't affect all resources)
- Regional deployment (Sentinel near managed resources)
- Different configurations per environment

**Example Multi-Instance Deployment**:

```
                            ┌───────────────────┐
                            │  HyperFleet API   │
                            └─────────┬─────────┘
                                      │
                              Step 1: fetch resources
                                      │
                                      ▼
┌──────────────────────┐  ┌──────────────────────┐  ┌──────────────────────┐
│   Sentinel US-East   │  │   Sentinel US-West   │  │   Sentinel EU-West   │
│  resource_selector:  │  │  resource_selector:  │  │  resource_selector:  │
│  - label: region     │  │  - label: region     │  │  - label: region     │
│    value: us-east    │  │    value: us-west    │  │    value: eu-west    │
└──────────┬───────────┘  └──────────┬───────────┘  └──────────┬───────────┘
           │                         │                         │
           │                         ▼                         │
           └─────────────► Step 2: publish events ◄────────────┘
                                     │
                                     ▼
                            ┌───────────────────┐
                            │  Message Broker   │
                            └───────────────────┘
```

**Important**: This is **NOT leader election**. Multiple Sentinels with overlapping resource selectors will produce duplicate events. Ensure selectors are **non-overlapping** to avoid event duplication. See [Known Limitations](#known-limitations).

#### Resource Selector Strategies

**Regional Partitioning**:

```yaml
# Sentinel A
resource_selector:
  - label: region
    value: us-east

# Sentinel B
resource_selector:
  - label: region
    value: us-west
```

**Environment Partitioning**:

```yaml
# Production Sentinel
resource_selector:
  - label: environment
    value: production

# Development Sentinel
resource_selector:
  - label: environment
    value: development
```

**Hybrid Partitioning**:

```yaml
# Production US-East
resource_selector:
  - label: region
    value: us-east
  - label: environment
    value: production
```

## Architecture Reference

For more details on the Sentinel architecture and resource filtering design, see the [architecture documentation](https://github.com/openshift-hyperfleet/architecture/tree/main/hyperfleet/components/sentinel).
