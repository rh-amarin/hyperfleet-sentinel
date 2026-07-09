# HyperFleet Sentinel Metrics

> **Audience:** Developers and SREs setting up monitoring for HyperFleet Sentinel.

This document describes the Prometheus metrics exposed by the HyperFleet Sentinel service for monitoring and observability.

## Metrics Overview

Sentinel exposes metrics on port 9090 at the `/metrics` endpoint. Metrics follow the `hyperfleet_sentinel_*` naming convention for sentinel-specific metrics and the `hyperfleet_broker_*` naming convention for broker metrics (provided by hyperfleet-broker).

## Common Labels

All metrics include the following labels:

| Label | Description | Example Values |
|-------|-------------|----------------|
| `resource_type` | Type of resource being monitored | `clusters`, `nodepools` |
| `resource_selector` | Label selector for resource filtering | `shard:1`, `env:prod`, `all` |

Additional labels may be present depending on the specific metric (see individual metric descriptions below).

## Metrics Catalog

### 1. `hyperfleet_sentinel_pending_resources`

**Type:** Gauge

**Description:** Current number of resources pending reconciliation based on max age intervals or generation mismatches. This gauge provides a snapshot of resources that need processing.

**Labels:**
- `resource_type`: Type of resource (e.g., `clusters`, `nodepools`)
- `resource_selector`: Label selector applied to the resources

**Use Cases:**
- Monitor backlog of pending resources
- Alert on high resource queue sizes
- Track resource processing efficiency

**Example Query:**
```promql
# Total pending resources across all types
sum(hyperfleet_sentinel_pending_resources)

# Pending resources by type
sum by (resource_type) (hyperfleet_sentinel_pending_resources)
```

---

### 2. `hyperfleet_sentinel_events_published_total`

**Type:** Counter

**Description:** Total number of reconciliation events successfully published to the message broker. Tracks event publication activity and throughput.

**Labels:**
- `resource_type`: Type of resource
- `resource_selector`: Label selector
- `reason`: Reason for publishing the event (e.g., `message decision matched`)

**Use Cases:**
- Monitor event publishing rate
- Track reconciliation triggers by reason
- Measure overall system activity

**Example Query:**
```promql
# Events published per second
rate(hyperfleet_sentinel_events_published_total[5m])

# Events by reason
sum by (reason) (rate(hyperfleet_sentinel_events_published_total[5m]))
```

---

### 3. `hyperfleet_sentinel_resources_skipped_total`

**Type:** Counter

**Description:** Total number of resources skipped during evaluation because they didn't meet the criteria for event publication (e.g., within max age, generation already reconciled).

**Labels:**
- `resource_type`: Type of resource
- `resource_selector`: Label selector
- `reason`: Reason for skipping (e.g., `message decision result is false`)

**Use Cases:**
- Monitor decision engine effectiveness
- Track resource skip rate vs publish rate
- Debug reconciliation loop behavior

**Example Query:**
```promql
# Resources skipped per second
rate(hyperfleet_sentinel_resources_skipped_total[5m])

# Skip ratio (skipped / total processed)
rate(hyperfleet_sentinel_resources_skipped_total[5m]) /
  (rate(hyperfleet_sentinel_resources_skipped_total[5m]) +
   rate(hyperfleet_sentinel_events_published_total[5m]))
```

---

### 4. `hyperfleet_sentinel_poll_duration_seconds`

**Type:** Histogram

**Description:** Duration of each polling cycle in seconds, including API calls, decision evaluation, and event publishing. Provides latency distribution for performance monitoring.

**Labels:**
- `resource_type`: Type of resource
- `resource_selector`: Label selector

**Buckets:** Default Prometheus buckets (0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10)

**Use Cases:**
- Monitor polling loop performance
- Detect API latency issues
- Set SLO targets for reconciliation speed

**Example Query:**
```promql
# 95th percentile poll duration
histogram_quantile(0.95,
  rate(hyperfleet_sentinel_poll_duration_seconds_bucket[5m]))

# Average poll duration
rate(hyperfleet_sentinel_poll_duration_seconds_sum[5m]) /
  rate(hyperfleet_sentinel_poll_duration_seconds_count[5m])
```

---

### 5. `hyperfleet_sentinel_api_errors_total`

**Type:** Counter

**Description:** Total number of errors when calling the HyperFleet API. Tracks API connectivity and availability issues.

**Labels:**
- `resource_type`: Type of resource
- `resource_selector`: Label selector
- `error_type`: Type of error (e.g., `fetch_error`, `timeout`, `auth_error`)

**Use Cases:**
- Alert on API availability issues
- Track error rates by type
- Monitor API health

**Example Query:**
```promql
# API error rate
rate(hyperfleet_sentinel_api_errors_total[5m])

# Errors by type
sum by (error_type) (rate(hyperfleet_sentinel_api_errors_total[5m]))
```

---

### 6. `hyperfleet_sentinel_broker_errors_total`

**Type:** Counter

**Description:** Total number of errors when publishing events to the message broker. Tracks broker connectivity and delivery issues.

**Labels:**
- `resource_type`: Type of resource
- `resource_selector`: Label selector
- `error_type`: Type of error (e.g., `publish_error`, `connection_error`, `timeout`)

**Use Cases:**
- Alert on message delivery failures
- Monitor broker health
- Track error rates by broker type

**Example Query:**
```promql
# Broker error rate
rate(hyperfleet_sentinel_broker_errors_total[5m])

# Errors by type
sum by (error_type) (rate(hyperfleet_sentinel_broker_errors_total[5m]))
```

---

### 7. `hyperfleet_sentinel_last_successful_poll_timestamp_seconds`

**Type:** Gauge

**Description:** Unix timestamp (seconds since epoch) of the last successful poll cycle completion. Used for deadman's switch monitoring to detect when the Sentinel service becomes unresponsive.

**Labels:**
- `component`: Always `sentinel`
- `version`: Sentinel version

> **Note:** This metric does not carry `resource_type` or `resource_selector` labels as it represents the overall Sentinel health, not a per-resource measurement.


**Use Cases:**
- Detect stale or hung polling loops
- Implement deadman's switch alerts

**Example Query:**
```promql
# Time since last successful poll
time() - hyperfleet_sentinel_last_successful_poll_timestamp_seconds

# Alert if stale for more than 60 seconds
time() - hyperfleet_sentinel_last_successful_poll_timestamp_seconds > 60
AND (hyperfleet_sentinel_last_successful_poll_timestamp_seconds > 0)
```

---
## Broker Metrics

The following metrics are automatically provided by the [hyperfleet-broker](https://github.com/openshift-hyperfleet/hyperfleet-broker) library (v1.1.0+). They are registered in the same Prometheus registry and exposed on the same `/metrics` endpoint.

### Common Labels

| Label | Description | Example Values |
|-------|-------------|----------------|
| `component` | Component using the broker | `sentinel` |
| `version` | Version of the component | `1.0.0`, `dev` |
| `topic` | Broker topic name | `hyperfleet.clusters.reconcile` |

### 1. `hyperfleet_broker_messages_published_total`

**Type:** Counter

**Description:** Total number of messages published to the broker. Automatically incremented by the broker library on each successful publish.

**Labels:**
- `topic`: Broker topic
- `component`: Component name
- `version`: Component version

**Example Query:**

```promql
# Published messages per second
rate(hyperfleet_broker_messages_published_total{component="sentinel"}[5m])
```

---

### 2. `hyperfleet_broker_errors_total`

**Type:** Counter

**Description:** Total number of message processing errors in the broker library. Covers conversion errors and publish failures.

**Labels:**
- `topic`: Broker topic
- `error_type`: Type of error (`conversion`, `publish`)
- `component`: Component name
- `version`: Component version

**Example Query:**

```promql
# Broker errors by type
sum by (error_type) (rate(hyperfleet_broker_errors_total{component="sentinel"}[5m]))
```

---

> **Note:** The broker library also registers consumer-side metrics (`messages_consumed_total`, `message_duration_seconds`) that are not documented here because the Sentinel only publishes messages. See the [hyperfleet-broker](https://github.com/openshift-hyperfleet/hyperfleet-broker) documentation for the full metric catalog.

---

## Grafana Dashboard

A pre-built Grafana dashboard is available at `deployments/dashboards/sentinel-metrics.json`. The dashboard includes:

1. **Pending Resources** - Current backlog gauge and time series
2. **Events Published Rate** - Event publication throughput by reason
3. **Resources Skipped Rate** - Resource skip rate by reason
4. **Poll Duration Percentiles** - p50, p95, p99 latency tracking
5. **Poll Rate by Resource Type** - Processing rate table
6. **API Errors Rate** - API error tracking by error type
7. **Broker Errors Rate** - Broker error tracking by error type

### Import Dashboard

To import the dashboard into Grafana:

1. Navigate to Grafana → Dashboards → Import
2. Upload `deployments/dashboards/sentinel-metrics.json`
3. Select your Prometheus datasource
4. Click "Import"

---

## Prometheus Integration

### Google Cloud Managed Prometheus (GMP) (Recommended)

Google Cloud Managed Prometheus is a fully managed Prometheus-compatible monitoring service for GKE. It automatically collects metrics using PodMonitoring resources.

#### 1. Enable Managed Prometheus

GMP is available on GKE clusters. Ensure managed collection is enabled:

```bash
# For GKE Autopilot, managed collection is enabled by default
# For GKE Standard, enable it with:
gcloud container clusters update CLUSTER_NAME \
  --enable-managed-prometheus \
  --zone=ZONE
```

#### 2. Deploy Sentinel with PodMonitoring

The Helm chart automatically creates a PodMonitoring resource when deployed:

```bash
helm install sentinel oci://quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel-chart \
  --namespace hyperfleet-system \
  --create-namespace \
  --set monitoring.podMonitoring.enabled=true
```

The PodMonitoring resource scrapes metrics from Sentinel pods automatically.

#### 3. Verify and Access Metrics

```bash
# Check PodMonitoring was created
kubectl get podmonitoring -n hyperfleet-system

# Verify the PodMonitoring details
kubectl describe podmonitoring sentinel -n hyperfleet-system
```

Access metrics in **Google Cloud Console → Metrics Explorer**:
1. Navigate to **Monitoring → Metrics Explorer**
2. In the resource type, select **Prometheus Target**
3. Query your metrics:
   ```promql
   hyperfleet_sentinel_pending_resources
   ```


### Prometheus Operator (OpenShift, vanilla Kubernetes)

For clusters using [Prometheus Operator](https://github.com/prometheus-operator/prometheus-operator) (common in OpenShift, Rancher, and vanilla Kubernetes environments), the Helm chart can create a ServiceMonitor resource.

#### 1. Enable ServiceMonitor

Deploy Sentinel with ServiceMonitor enabled:

```bash
helm install sentinel oci://quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel-chart \
  --namespace hyperfleet-system \
  --create-namespace \
  --set monitoring.serviceMonitor.enabled=true
```

#### 2. Match Prometheus Selector Labels

Most Prometheus Operator installations require ServiceMonitors to have specific labels to be picked up. Check your Prometheus instance's `serviceMonitorSelector`:

```bash
kubectl get prometheus -A -o jsonpath='{.items[*].spec.serviceMonitorSelector}'
```

Then set the matching labels:

```bash
helm install sentinel oci://quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel-chart \
  --namespace hyperfleet-system \
  --set monitoring.serviceMonitor.enabled=true \
  --set monitoring.serviceMonitor.additionalLabels.release=prometheus
```

#### 3. Verify Metrics Collection

```bash
# Check ServiceMonitor was created
kubectl get servicemonitor -n hyperfleet-system

# Verify the ServiceMonitor details (name follows Helm release: <release>-sentinel)
kubectl get servicemonitor -n hyperfleet-system -l app.kubernetes.io/name=sentinel

# Verify the Service exposes the metrics port
kubectl get svc -n hyperfleet-system -l app.kubernetes.io/name=sentinel
```

#### ServiceMonitor Configuration Options

| Value | Default | Description |
|-------|---------|-------------|
| `monitoring.serviceMonitor.enabled` | `false` | Create ServiceMonitor resource |
| `monitoring.serviceMonitor.interval` | `30s` | Scrape interval |
| `monitoring.serviceMonitor.scrapeTimeout` | `10s` | Scrape timeout (must be less than interval) |
| `monitoring.serviceMonitor.additionalLabels` | `{}` | Labels for Prometheus selector matching |
| `monitoring.serviceMonitor.namespaceSelector` | `{}` | Target namespaces for cross-namespace monitoring |
| `monitoring.serviceMonitor.honorLabels` | `true` | Honor labels from target to avoid overwriting |
| `monitoring.serviceMonitor.metricRelabeling` | `[]` | Metric relabel configs |
| `monitoring.serviceMonitor.namespace` | `""` | Override namespace for ServiceMonitor creation |

> **Note**: When `monitoring.serviceMonitor.namespace` is set, a `namespaceSelector.matchNames` is automatically added pointing to the release namespace, so Prometheus can discover the Service across namespaces. To use a custom `namespaceSelector` instead, set `monitoring.serviceMonitor.namespaceSelector` explicitly.

#### Coexistence with PodMonitoring

ServiceMonitor and PodMonitoring can be enabled simultaneously for environments that support both. Each resource is independently gated:

- **GKE with GMP**: Use `monitoring.podMonitoring.enabled=true`
- **OpenShift / vanilla Kubernetes**: Use `monitoring.serviceMonitor.enabled=true`
- **Hybrid**: Both can be enabled without conflict

---

## Querying Metrics

### Google Cloud Console

Query metrics through the Google Cloud Console:

1. **Navigate to the Google Cloud Console**
2. **Go to: Monitoring → Metrics Explorer**
3. **Query your metrics**, for example:
   ```promql
   hyperfleet_sentinel_pending_resources
   rate(hyperfleet_sentinel_events_published_total[5m])
   ```

This uses the integrated Metrics Explorer and provides a native GKE experience.

### Verify Metrics Collection

Check that metrics are being collected:

```bash
# Verify PodMonitoring exists
kubectl get podmonitoring sentinel -n hyperfleet-system

# Check Service endpoints
kubectl get endpoints sentinel -n hyperfleet-system

# View recent metrics in Google Cloud Console
# Navigate to: Monitoring → Metrics Explorer
# Query: hyperfleet_sentinel_pending_resources
```

### Metrics Architecture

```text
[Sentinel Pod] → [Service (ClusterIP):9090] → [PodMonitoring] → [Prometheus] → [Metrics Explorer] → [Google Cloud Console]
```

Metrics are collected internally by Google Cloud Managed Prometheus (GMP) via the PodMonitoring.

---

## Best Practices

1. **Set up alerts** for critical metrics (API errors, broker errors, high pending resources)
2. **Monitor trends** over time to understand baseline behavior
3. **Use labels** to filter metrics by shard or resource type for granular visibility
4. **Correlate metrics** with application logs for debugging
5. **Review dashboards** regularly during incidents and normal operations
6. **Adjust thresholds** based on your specific SLO/SLA requirements

---

## Troubleshooting

### Metrics not appearing in Google Cloud Console

**GKE with Google Cloud Managed Prometheus:**

1. **Verify Managed Prometheus is enabled:**
   ```bash
   gcloud container clusters describe CLUSTER_NAME \
     --zone=ZONE \
     --format="value(monitoringConfig.managedPrometheusConfig.enabled)"
   # Should return: true
   ```

2. **Check if PodMonitoring is created:**
   ```bash
   kubectl get podmonitoring -n hyperfleet-system
   kubectl describe podmonitoring sentinel -n hyperfleet-system
   ```

3. **Verify Service and endpoints:**
   ```bash
   kubectl get svc sentinel -n hyperfleet-system
   kubectl get endpoints sentinel -n hyperfleet-system
   # Should show pod IP:9090
   ```

4. **Review GMP collector logs:**
   ```bash
   kubectl logs -n gmp-system -l app.kubernetes.io/name=collector
   # Look for scrape errors or connection issues
   ```

5. **Test metrics endpoint directly:**
   ```bash
   kubectl port-forward -n hyperfleet-system svc/sentinel 9090:9090
   # In another terminal:
   curl http://localhost:9090/metrics | grep hyperfleet_sentinel
   ```

6. **Check if metrics appear in Google Cloud Console:**
   - Navigate to: **Monitoring → Metrics Explorer**
   - Select resource type: **Prometheus Target**
   - Query: `hyperfleet_sentinel_pending_resources`


### Dashboard shows no data

1. Verify the datasource is configured correctly
2. Check the time range in Grafana
3. Ensure metrics are being scraped by Prometheus
4. Verify namespace and resource_type variables are set correctly

### High cardinality warnings

If you see warnings about high cardinality:
- Review the number of unique label combinations
- Consider reducing the number of shards or resource selectors
- Contact the HyperFleet team for guidance

---

## Additional Resources

- [Prometheus Query Language (PromQL)](https://prometheus.io/docs/prometheus/latest/querying/basics/)
- [Grafana Dashboards](https://grafana.com/docs/grafana/latest/dashboards/)
- [Google Cloud Managed Prometheus](https://cloud.google.com/stackdriver/docs/managed-prometheus)
- [PodMonitoring API Reference](https://github.com/GoogleCloudPlatform/prometheus-engine/blob/main/doc/api.md#podmonitoring)
- [HyperFleet Architecture Documentation](https://github.com/openshift-hyperfleet/architecture)
