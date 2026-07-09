# Deployment Guide

> **Audience:** Operators deploying and running Sentinel on Kubernetes.

## Full-Stack Deployment

To deploy the complete HyperFleet control plane (API, Sentinel, Adapters, Broker), see [hyperfleet-infra](https://github.com/openshift-hyperfleet/hyperfleet-infra). The rest of this guide covers standalone Sentinel Helm deployment.

## Installation

```bash
helm install sentinel oci://quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel-chart \
  --namespace hyperfleet-system \
  --create-namespace \
  --set image.registry=quay.io \
  --set image.repository=redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel \
  --set image.tag=<version>
```

See [Helm Values Reference](../charts/README.md) for all available values.

## Configuration Overview

Helm values render the deployment manifests, while Sentinel resolves runtime configuration in this order:
1. **CLI flags**
2. **Environment variables** (`HYPERFLEET_*`)
3. **YAML config file** mounted from the ConfigMap

For the full configuration schema (YAML fields, CLI flags, environment variable mappings), see [Configuration Reference](config.md).

For the CEL decision engine concepts and operator guidance, see [Operator Guide](sentinel-operator-guide.md).

## Choosing a Broker

| Broker | When to use | Key Helm values |
|--------|-------------|-----------------|
| **RabbitMQ** | On-prem, local dev, self-hosted environments | `broker.type=rabbitmq`, `broker.rabbitmq.url` |
| **Google Pub/Sub** | GCP environments, managed messaging | `broker.type=googlepubsub`, `broker.googlepubsub.projectId` |

See the [broker library](https://github.com/openshift-hyperfleet/hyperfleet-broker) for implementation details.

## Choosing a Monitoring Backend

| Environment | Resource | Helm value |
|-------------|----------|------------|
| GKE with Google Cloud Managed Prometheus | PodMonitoring | `monitoring.podMonitoring.enabled=true` |
| Prometheus Operator (OpenShift, vanilla K8s) | ServiceMonitor | `monitoring.serviceMonitor.enabled=true` |

Both can coexist for hybrid environments. For metrics details, see [Metrics](metrics.md). For alert rules, see [Alerts](alerts.md).

## Tracing

Distributed tracing is disabled by default. Enable via Helm:

```bash
--set tracing.enabled=true \
--set tracing.otlpEndpoint=<collector-endpoint>
```

| Helm value | Description | Default |
|------------|-------------|---------|
| `tracing.enabled` | Enable trace export | `false` |
| `tracing.otlpEndpoint` | OTLP collector endpoint (stdout when empty) | `""` |
| `tracing.otlpProtocol` | `grpc` or `http/protobuf` | `grpc` |
| `tracing.sampler` | Sampler type | `parentbased_traceidratio` |
| `tracing.samplerArg` | Sampling rate (`1.0` for dev, `0.01` for prod) | `1.0` |

## GKE-Specific Setup

### Workload Identity for Pub/Sub

When using Google Pub/Sub on GKE, grant the Pub/Sub publisher role to the Kubernetes ServiceAccount via Workload Identity Federation:

```bash
export GCP_PROJECT=<your-project>
export GCP_PROJECT_NUMBER=$(gcloud projects describe ${GCP_PROJECT} --format="value(projectNumber)")
export NAMESPACE=<your-namespace>

gcloud projects add-iam-policy-binding ${GCP_PROJECT} \
  --role="roles/pubsub.publisher" \
  --member="principal://iam.googleapis.com/projects/${GCP_PROJECT_NUMBER}/locations/global/workloadIdentityPools/${GCP_PROJECT}.svc.id.goog/subject/ns/${NAMESPACE}/sa/<release-name>-hyperfleet-sentinel" \
  --condition=None
```

> **Note**: The `principal://` format is:
> `principal://iam.googleapis.com/projects/{PROJECT_NUMBER}/locations/global/workloadIdentityPools/{PROJECT_ID}.svc.id.goog/subject/ns/{NAMESPACE}/sa/{K8S_SA_NAME}`

#### Verify Workload Identity IAM Binding

If the pod fails to authenticate with Pub/Sub, verify the IAM binding exists:

```bash
gcloud projects get-iam-policy ${GCP_PROJECT} \
  --flatten="bindings[].members" \
  --filter="bindings.members:principal://iam.googleapis.com/projects/${GCP_PROJECT_NUMBER}" \
  --format="table(bindings.role, bindings.members)"
```

You should see an entry with `roles/pubsub.publisher` for your namespace/SA.

#### Remove Workload Identity IAM Binding

When decommissioning a Sentinel instance, remove the IAM binding:

```bash
gcloud projects remove-iam-policy-binding ${GCP_PROJECT} \
  --role="roles/pubsub.publisher" \
  --member="principal://iam.googleapis.com/projects/${GCP_PROJECT_NUMBER}/locations/global/workloadIdentityPools/${GCP_PROJECT}.svc.id.goog/subject/ns/${NAMESPACE}/sa/<release-name>-hyperfleet-sentinel"
```

### Google Cloud Managed Prometheus

Enable PodMonitoring for automatic metric scraping:

```bash
--set monitoring.podMonitoring.enabled=true
```

Verify metrics appear in [Metrics Explorer](https://console.cloud.google.com/monitoring/metrics-explorer) by searching for `hyperfleet_sentinel`.

## Deployment Examples

### Minimal — RabbitMQ

```bash
helm install sentinel oci://quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel-chart \
  --namespace hyperfleet-system \
  --create-namespace \
  --set image.registry=quay.io \
  --set image.repository=redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel \
  --set image.tag=v1.0.0 \
  --set broker.type=rabbitmq \
  --set broker.rabbitmq.url="amqp://<username>:<password>@rabbitmq.hyperfleet-system.svc.cluster.local:5672/hyperfleet"
```

### Production — Pub/Sub with Sharding and Tracing

```bash
helm install sentinel-shard1 oci://quay.io/redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel-chart \
  --namespace hyperfleet-system \
  --create-namespace \
  --set image.registry=quay.io \
  --set image.repository=redhat-services-prod/hyperfleet-tenant/hyperfleet/hyperfleet-sentinel \
  --set image.tag=v1.0.0 \
  --set broker.type=googlepubsub \
  --set broker.googlepubsub.projectId=my-gcp-project \
  --set config.resourceSelector[0].label=shard \
  --set config.resourceSelector[0].value=1 \
  --set monitoring.podMonitoring.enabled=true \
  --set monitoring.prometheusRule.enabled=true \
  --set tracing.enabled=true \
  --set tracing.otlpEndpoint=otel-collector.observability.svc:4317
```

For horizontal scaling patterns and multi-instance deployment, see [Scaling](multi-instance-deployment.md).

## Verifying the Deployment

### Pod Status

```bash
kubectl get pods -n <namespace> -l app.kubernetes.io/name=hyperfleet-sentinel
```

### Health Endpoints

```bash
kubectl port-forward -n <namespace> svc/<release-name>-hyperfleet-sentinel 8080:8080 9090:9090
curl http://localhost:8080/healthz   # liveness — detects poll staleness
curl http://localhost:8080/readyz    # readiness — 503 until broker + first poll succeed
```

### Metrics

```bash
kubectl port-forward -n <namespace> svc/<release-name>-hyperfleet-sentinel 8080:8080 9090:9090
curl http://localhost:9090/metrics | grep hyperfleet_sentinel
```

### Logs

```bash
kubectl logs -n <namespace> -l app.kubernetes.io/name=hyperfleet-sentinel -f
```

Startup logs confirm configuration loaded, resource type, and broker connection.

## Related Documentation

- [Helm Values Reference](../charts/README.md) — all chart values
- [Configuration Reference](config.md) — YAML schema, CLI flags, env vars
- [Operator Guide](sentinel-operator-guide.md) — decision engine, CEL expressions
- [Scaling](multi-instance-deployment.md) — horizontal sharding
- [Running on GKE](sentinel-for-gke-dev.md#running-on-gke) — dev GKE procedures (image build, Helm dev deploy, cleanup)
- [Runbook](runbook.md) — reliability, failure recovery
