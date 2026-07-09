# HyperFleet Sentinel Alerts

> **Audience:** Developers and SREs setting up monitoring for HyperFleet Sentinel.

---

## Purpose

This document provides ready alert rules that integrate with Prometheus to ensure reliable monitoring and incident response.

---

## Alert Rules Reference

The following 8 alert rules provide comprehensive monitoring for production Sentinel deployments.

### Critical Alerts

#### SentinelDown
```yaml
alert: SentinelDown
expr: absent(up{service="<your-sentinel-service-name>"}) or up{service="<your-sentinel-service-name>"} == 0
for: 5m
labels:
  severity: critical
  component: sentinel
annotations:
  summary: "Sentinel service is down"
  description: "Sentinel metrics endpoint is not responding. Service may be down or unreachable."
```
**Impact**: Resource reconciliation stopped completely.

**Response**: Check pod status, logs, and resource constraints.

#### SentinelAPIErrorRateHigh
```yaml
alert: SentinelAPIErrorRateHigh
expr: rate(hyperfleet_sentinel_api_errors_total[5m]) > 0.1
for: 5m
labels:
  severity: critical
  component: sentinel
annotations:
  summary: "High API error rate in Sentinel"
  description: "Sentinel is experiencing {{ $value }} API errors/sec for resource_type {{ $labels.resource_type }}. Check HyperFleet API availability."
```
**Impact**: Unable to fetch resource status, reconciliation decisions based on stale data.

**Response**: Check HyperFleet API service health and network connectivity.

#### SentinelBrokerErrorRateHigh
```yaml
alert: SentinelBrokerErrorRateHigh
expr: rate(hyperfleet_sentinel_broker_errors_total[5m]) > 0.05
for: 5m
labels:
  severity: critical
  component: sentinel
annotations:
  summary: "High broker error rate in Sentinel"
  description: "Sentinel is experiencing {{ $value }} broker errors/sec for resource_type {{ $labels.resource_type }}. Check message broker connectivity."
```
**Impact**: Events not reaching adapters, reconciliation loops broken.

**Response**: Check message broker health and Sentinel broker configuration.

#### SentinelPollStale
```yaml
alert: SentinelPollStale
expr: |
  hyperfleet_sentinel_last_successful_poll_timestamp_seconds > 0
  and time() - hyperfleet_sentinel_last_successful_poll_timestamp_seconds > 60
for: 1m
labels:
  severity: critical
  component: sentinel
annotations:
  summary: "Sentinel poll loop is stale"
  description: "Sentinel has not completed a successful poll cycle in over 60 seconds. The service may be hung or unable to poll."
```
**Impact**: Complete polling failure, no reconciliation events generated.

**Response**: Check Sentinel logs and restart if necessary.

### Warning Alerts

#### SentinelSlowPolling
```yaml
alert: SentinelSlowPolling
expr: histogram_quantile(0.95, rate(hyperfleet_sentinel_poll_duration_seconds_bucket[5m])) > 5
for: 10m
labels:
  severity: warning
  component: sentinel
annotations:
  summary: "Sentinel polling cycles are slow"
  description: "95th percentile poll duration is {{ $value }}s for {{ $labels.resource_type }}. This may indicate API latency or processing issues."
```
**Impact**: Delayed reconciliation, potentially missing max age intervals.

**Response**: Check resource count growth and API performance.

#### SentinelNoEventsPublished
```yaml
alert: SentinelNoEventsPublished
expr: |
  hyperfleet_sentinel_pending_resources > 0
  unless on(resource_type, resource_selector)
  rate(hyperfleet_sentinel_events_published_total[15m]) > 0
for: 15m
labels:
  severity: warning
  component: sentinel
annotations:
  summary: "Sentinel not publishing events"
  description: "Sentinel has pending resources but hasn't published any events in 15 minutes. Service may be stuck."
```
**Impact**: Resources may be stuck without reconciliation events.

**Response**: Check decision engine logic and adapter status updates.

#### SentinelHighPendingResources
```yaml
alert: SentinelHighPendingResources
expr: sum(hyperfleet_sentinel_pending_resources) > 100
for: 10m
labels:
  severity: warning
  component: sentinel
annotations:
  summary: "High number of pending resources in Sentinel"
  description: "{{ $value }} resources are pending reconciliation for more than 10 minutes. This may indicate processing bottleneck or API issues."
```
**Impact**: May indicate capacity issues or API problems.

**Response**: Check resource count growth and consider horizontal scaling.

### Informational Alerts

#### SentinelHighSkipRatio
```yaml
alert: SentinelHighSkipRatio
expr: |
  (
    rate(hyperfleet_sentinel_resources_skipped_total[10m]) /
    (rate(hyperfleet_sentinel_resources_skipped_total[10m]) +
     rate(hyperfleet_sentinel_events_published_total[10m]))
  ) > 0.95
for: 30m
labels:
  severity: info
  component: sentinel
annotations:
  summary: "High resource skip ratio in Sentinel"
  description: "{{ $value | humanizePercentage }} of resources are being skipped. This may indicate message_decision configuration issues."
```
**Impact**: May indicate decision thresholds too restrictive or adapter status update issues.

**Response**: Review message_decision configuration and adapter health.

---

### Alerting with Google Cloud Managed Prometheus

**Note**: Google Cloud Managed Prometheus uses Google Cloud Alerting for alert management, not PrometheusRule CRDs. The alerting rules below are provided as PromQL expressions that you can configure in Google Cloud Console → Monitoring → Alerting.

For Prometheus Operator compatibility, the Helm chart can optionally deploy a PrometheusRule resource when `monitoring.prometheusRule.enabled=true` (disabled by default for GMP). The following alert examples can be configured in either system:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: sentinel-alerts
  namespace: hyperfleet-system
spec:
  groups:
  - name: sentinel.rules
    interval: 30s
    rules:
    - alert: SentinelHighPendingResources
      expr: |
        sum(hyperfleet_sentinel_pending_resources) > 100
      for: 10m
      labels:
        severity: warning
        component: sentinel
      annotations:
        summary: "High number of pending resources"
        description: "{{ $value }} resources are pending reconciliation for more than 10 minutes."

    - alert: SentinelAPIErrorRateHigh
      expr: |
        rate(hyperfleet_sentinel_api_errors_total[5m]) > 0.1
      for: 5m
      labels:
        severity: critical
        component: sentinel
      annotations:
        summary: "High API error rate detected"
        description: "API error rate is {{ $value | humanize }} errors/sec for resource_type {{ $labels.resource_type }}."

    - alert: SentinelBrokerErrorRateHigh
      expr: |
        rate(hyperfleet_sentinel_broker_errors_total[5m]) > 0.05
      for: 5m
      labels:
        severity: critical
        component: sentinel
      annotations:
        summary: "High broker error rate detected"
        description: "Broker error rate is {{ $value | humanize }} errors/sec for resource_type {{ $labels.resource_type }}."

    - alert: SentinelSlowPolling
      expr: |
        histogram_quantile(0.95,
          rate(hyperfleet_sentinel_poll_duration_seconds_bucket[5m])) > 5
      for: 10m
      labels:
        severity: warning
        component: sentinel
      annotations:
        summary: "Polling cycles are slow"
        description: "95th percentile poll duration is {{ $value | humanize }}s for {{ $labels.resource_type }}."

    - alert: SentinelNoEventsPublished
      expr: |
        hyperfleet_sentinel_pending_resources > 0
        unless on(resource_type, resource_selector)
        rate(hyperfleet_sentinel_events_published_total[15m]) > 0
      for: 15m
      labels:
        severity: warning
        component: sentinel
      annotations:
        summary: "No events published despite pending resources"
        description: "Sentinel has pending resources but hasn't published events in 15 minutes."
    
    - alert: SentinelPollStale
      expr: |
        hyperfleet_sentinel_last_successful_poll_timestamp_seconds > 0
        and time() - hyperfleet_sentinel_last_successful_poll_timestamp_seconds > 60
      for: 1m
      labels:
        severity: critical
        component: sentinel
      annotations:
        summary: "Sentinel poll loop is stale"
        description: "Sentinel has not completed a successful poll cycle in over 60 seconds."
    - alert: SentinelDown
      expr: absent(up{service="{{ include "sentinel.fullname" . }}"}) or up{service="{{ include "sentinel.fullname" . }}"} == 0
      for: 5m
      labels:
        severity: critical
        component: sentinel
      annotations:
        summary: "Sentinel service is down"
        description: "Sentinel metrics endpoint is not responding. Service may be down or unreachable."
    - alert: SentinelHighSkipRatio
      expr: |
        (
          rate(hyperfleet_sentinel_resources_skipped_total[10m]) /
          (rate(hyperfleet_sentinel_resources_skipped_total[10m]) +
          rate(hyperfleet_sentinel_events_published_total[10m]))
        ) > 0.95
      for: 30m
      labels:
        severity: info
        component: sentinel
      annotations:
        summary: "High resource skip ratio in Sentinel"
        description: "{{ $value | humanizePercentage }} of resources are being skipped. This may indicate message_decision configuration issues."
```

To configure these alerts in **Google Cloud Console**:
1. Navigate to **Monitoring → Alerting**
2. Click **Create Policy**
3. Use the PromQL expressions above in the condition configuration
4. Configure notification channels and documentation

For Prometheus Operator users, enable PrometheusRule in values.yaml and verify:

```bash
kubectl get prometheusrule -n hyperfleet-system
```
