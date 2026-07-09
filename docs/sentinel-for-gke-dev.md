# Running Sentinel

> **Audience:** Developers running Sentinel on GKE for integration testing before merging code changes.

> **IMPORTANT**: This documentation covers running Sentinel for **development and testing purposes**. Production deployments are handled via [hyperfleet-infra](https://github.com/openshift-hyperfleet/hyperfleet-infra). For local development, see the [Development Guide](development.md). For production deployment, see the [Deployment Guide](deployment.md).

## Table of Contents

- [Running on GKE](#running-on-gke)
  - [Prerequisites](#prerequisites-for-gke)
  - [Set Up Environment Variables](#1-set-up-environment-variables)
  - [Connect to GKE Cluster](#2-connect-to-gke-cluster)
  - [Building Container Image](#3-building-container-image)
  - [Authentication and Image Push](#4-authentication-and-image-push)
  - [Configure Workload Identity](#5-configure-workload-identity)
  - [Helm Deployment](#6-helm-deployment)
  - [Verification Steps](#7-verification-steps)
  - [Cleanup](#8-cleanup)
- [Troubleshooting](#troubleshooting)

---

## Running on GKE

### Prerequisites for GKE

- GKE cluster access (use the shared cluster below or your own)
- GKE cluster must have Workload Identity enabled (required for Pub/Sub
  authentication; the shared dev cluster already has this enabled)
- `gcloud` CLI configured and authenticated
- `kubectl` configured for the cluster
- `podman` for building images
- `helm` for deploying the chart
- Access to Google Container Registry (GCR) for your project

### 1. Set Up Environment Variables

Set these variables once and use them throughout the deployment:

```bash
# GCP project ID
export GCP_PROJECT=hcm-hyperfleet

# Your namespace: hyperfleet-{env}-{username}
export NAMESPACE=hyperfleet-dev-${USER}

# Image tag: {namespace}-{git-sha-short} (follows naming convention)
export IMAGE_TAG=${NAMESPACE}-$(git rev-parse --short HEAD)
# Example: hyperfleet-dev-rafael-a1b2c3d (if USER=rafael)
```

> **Note**: The image tag format `{namespace}-{git-sha-short}` follows the [Naming Strategy](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/components/sentinel/sentinel-naming-strategy.md) convention to prevent collisions between developers.

### 2. Connect to GKE Cluster

A shared GKE cluster with Config Connector enabled is available for development and testing:

```bash
gcloud container clusters get-credentials hyperfleet-dev --zone=us-central1-a --project=${GCP_PROJECT}
```

**Usage guidelines:**

- For personal work, create a namespace named after yourself to isolate resources
- For team collaboration, use a designated namespace to separate resources among members

> **Note**: This environment is scheduled for deletion every Friday at 8:00 PM (EST). See [GKE deployment docs](https://github.com/openshift-hyperfleet/architecture/tree/main/hyperfleet/deployment/GKE) for more details.

### 3. Building Container Image

#### Option A: Using Makefile Targets (Recommended for Dev)

For pushing to your personal Quay registry:

```bash
# One-time login (required before pushing to Quay)
make quay-login

# Build and push to quay.io/${QUAY_USER}/${IMAGE_NAME}:dev-<commit>
QUAY_USER=${USER} make image-dev
```

This will output the image tag to use in your terraform.tfvars.

#### Option B: Manual Build for GCR

For pushing to Google Container Registry:

```bash
# Build for AMD64 (required for GKE)
podman build --platform linux/amd64 -t gcr.io/${GCP_PROJECT}/sentinel:${IMAGE_TAG} .
```

> **Note**: If building on ARM64 Mac for AMD64 GKE, you must use `--platform linux/amd64` to avoid architecture mismatch errors.

### 4. Authentication and Image Push

> **Note**: If you used `make image-dev` (Option A above), authentication and push are handled automatically. Skip to [Helm Deployment](#6-helm-deployment). For Quay.io, ensure you've run `make quay-login` first.

#### Configure Authentication with GCR

```bash
gcloud auth configure-docker gcr.io
```

#### Push Image to Registry

```bash
podman push gcr.io/${GCP_PROJECT}/sentinel:${IMAGE_TAG}
```

### 5. Configure Workload Identity

Follow the [Workload Identity for Pub/Sub](deployment.md#workload-identity-for-pubsub) instructions in the Deployment Guide. When running the `gcloud` command, use `sentinel-test` as the ServiceAccount name (matching the Helm release name in this guide).

### 6. Helm Deployment

Deploy Sentinel using the image you built:

#### Option A: Using Quay Image (from `make image-dev`)

If you used `make image-dev`, deploy with:

```bash
helm upgrade --install sentinel-test ./charts \
  --namespace ${NAMESPACE} \
  --create-namespace \
  --set global.imageRegistry=quay.io \
  --set image.repository=${USER}/hyperfleet-sentinel \
  --set image.tag=dev-$(git rev-parse --short HEAD) \
  --set broker.type=googlepubsub \
  --set broker.googlepubsub.projectId=${GCP_PROJECT} \
  --set monitoring.podMonitoring.enabled=true
```

#### Option B: Using GCR Image (from manual build)

If you manually built and pushed to GCR, deploy with:

```bash
helm upgrade --install sentinel-test ./charts \
  --namespace ${NAMESPACE} \
  --create-namespace \
  --set image.repository=gcr.io/${GCP_PROJECT}/sentinel \
  --set image.tag=${IMAGE_TAG} \
  --set broker.type=googlepubsub \
  --set broker.googlepubsub.projectId=${GCP_PROJECT} \
  --set monitoring.podMonitoring.enabled=true

# For Prometheus Operator environments (OpenShift, vanilla Kubernetes):
helm upgrade --install sentinel-test ./charts \
  --namespace ${NAMESPACE} \
  --create-namespace \
  --set image.repository=gcr.io/${GCP_PROJECT}/sentinel \
  --set image.tag=${IMAGE_TAG} \
  --set broker.type=googlepubsub \
  --set broker.googlepubsub.projectId=${GCP_PROJECT} \
  --set monitoring.serviceMonitor.enabled=true \
  --set monitoring.serviceMonitor.additionalLabels.release=prometheus
```

> **Tip**: The default topic is `{namespace}-{resourceType}`
> (e.g., `hyperfleet-dev-rafael-clusters`). Override with
> `--set broker.topic=custom-topic`. See
> [Naming Strategy](https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/components/sentinel/sentinel-naming-strategy.md)
> for details.

### 7. Verification Steps

#### Check Pod Status

```bash
kubectl get pods -n ${NAMESPACE} -l app.kubernetes.io/name=sentinel
```

#### View Pod Logs

```bash
kubectl logs -n ${NAMESPACE} -l app.kubernetes.io/name=sentinel -f
```

You should see the startup messages:

```log
2025-12-17T14:07:30.136547Z INFO [sentinel] [0.1.0] [pod-name] Loading configuration from /app/configs/sentinel.yaml
2025-12-17T14:07:30.137373Z INFO [sentinel] [0.1.0] [pod-name] Configuration loaded successfully: resource_type=clusters
2025-12-17T14:07:30.137382Z INFO [sentinel] [0.1.0] [pod-name] Starting HyperFleet Sentinel
```

> **Note**: Sentinel outputs minimal logs during normal operation. Use the health endpoints (`/healthz`, `/readyz`) and metrics to verify the service is running correctly. Configure `--log-format=json` for production deployments.

#### Verify Health Endpoints

Start port-forward in a separate terminal:

```bash
kubectl port-forward -n ${NAMESPACE} svc/sentinel-test 8080:8080 9090:9090
```

Check health endpoints:

```bash
# Liveness
curl http://localhost:8080/healthz

# Readiness
curl http://localhost:8080/readyz
```

Check metrics:

```bash
curl http://localhost:9090/metrics | grep hyperfleet_sentinel
```

#### Check Monitoring Resources

For GKE with Google Cloud Managed Prometheus (PodMonitoring):

```bash
kubectl get podmonitoring -n ${NAMESPACE}
kubectl describe podmonitoring -n ${NAMESPACE} -l app.kubernetes.io/name=sentinel
```

For Prometheus Operator environments (ServiceMonitor):

```bash
kubectl get servicemonitor -n ${NAMESPACE}
kubectl describe servicemonitor -n ${NAMESPACE} -l app.kubernetes.io/name=sentinel
```

#### Verify Metrics in Google Cloud Console

1. Open the [Metrics Explorer](https://console.cloud.google.com/monitoring/metrics-explorer) for your project
2. In "Select a metric", search for `hyperfleet_sentinel`
3. Select **Prometheus Target** > **Hyperfleet** > choose a metric (e.g., `api_errors_total`)

#### Verify Workload Identity IAM Binding

See [Verify Workload Identity IAM Binding](deployment.md#verify-workload-identity-iam-binding) in the Deployment Guide.

### 8. Cleanup

Remove the deployment when done:

```bash
helm uninstall sentinel-test -n ${NAMESPACE}
```

Optionally, delete the image from the registry:

```bash
gcloud container images delete gcr.io/${GCP_PROJECT}/sentinel:${IMAGE_TAG} \
  --quiet --force-delete-tags
```

If you configured Workload Identity, remove the IAM binding — see [Remove Workload Identity IAM Binding](deployment.md#remove-workload-identity-iam-binding) in the Deployment Guide.

---

## Troubleshooting

### Exec format error

**Problem**: Container fails to start with `exec format error`

**Cause**: Architecture mismatch - image was built for a different CPU architecture than the target

**Solution**: Ensure `--platform linux/amd64` is used when building:

```bash
podman build --platform linux/amd64 -t gcr.io/${GCP_PROJECT}/sentinel:${IMAGE_TAG} .
```

### Broker connection refused

**Problem**: Sentinel fails to start with "connection refused" errors for broker

**Cause**: Broker is not running or `broker.yaml` is configured for the wrong broker type

**Solution**:

1. Verify the broker is running (RabbitMQ or Pub/Sub emulator)
2. Ensure `broker.yaml` has the correct `type` (rabbitmq or googlepubsub)
3. For Pub/Sub emulator, ensure `PUBSUB_EMULATOR_HOST` is set
4. For RabbitMQ, ensure `BROKER_RABBITMQ_URL` is set or the URL in `broker.yaml` is correct

### Metrics not appearing in GMP

**Problem**: Metrics are not visible in Google Cloud Metrics Explorer

**Cause**: PodMonitoring not configured correctly or GMP collector not scraping

**Solution**:

1. Verify PodMonitoring is created:

   ```bash
   kubectl get podmonitoring -n ${NAMESPACE}
   ```

2. Check GMP collector logs:

   ```bash
   kubectl logs -n gmp-system -l app.kubernetes.io/name=collector
   ```

3. Ensure the metrics endpoint is accessible:

   ```bash
   kubectl port-forward -n ${NAMESPACE} svc/sentinel-test 8080:8080 9090:9090
   curl http://localhost:9090/metrics
   ```

### ConfigMap vs Environment Variable Configuration

**Problem**: Broker credentials not being picked up

**Cause**: Broker credentials must be set via environment variables, not ConfigMap

**Solution**: Use `--set` flags or a values file to set broker credentials:

```bash
--set broker.rabbitmq.url="amqp://user:pass@host:5672/"
```

### HyperFleet API Connection Errors

**Problem**: Sentinel cannot connect to HyperFleet API

**Solution**:

1. Verify the API endpoint is correct in your config
2. For local execution, ensure the API is running
3. For GKE, use the in-cluster service name:

   ```yaml
   clients:
     hyperfleet_api:
       base_url: http://hyperfleet-api.hyperfleet-system.svc.cluster.local:8080
   ```

### OpenAPI Client Not Generated

**Problem**: Build fails with missing package errors

**Cause**: OpenAPI client was not generated

**Solution**: Run the generate target before building:

```bash
make generate
make build
```
