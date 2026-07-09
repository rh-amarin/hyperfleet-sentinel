include .bingo/Variables.mk

.DEFAULT_GOAL := help

GO ?= go
GOFMT ?= gofmt

# Schema variant for OpenAPI generation (core, gcp)
VARIANT ?= core

# Binary output directory and name
BIN_DIR := bin
BINARY_NAME := $(BIN_DIR)/sentinel


# Version information
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_SHA ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_DIRTY ?= $(shell [ -z "$$(git status --porcelain 2>/dev/null)" ] || echo "-modified")
APP_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")

# Go build flags
GOFLAGS ?= -trimpath
LDFLAGS := -s -w \
           -X main.version=$(APP_VERSION) \
           -X main.commit=$(GIT_SHA) \
           -X main.date=$(BUILD_DATE)

# Container tool (docker or podman)
CONTAINER_TOOL ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

# =============================================================================
# Image Configuration
# =============================================================================
PLATFORM ?= linux/amd64
IMAGE_REGISTRY ?= quay.io/openshift-hyperfleet
IMAGE_NAME ?= hyperfleet-sentinel
IMAGE_TAG ?= $(APP_VERSION)

# Dev image configuration - set QUAY_USER to push to personal registry
# Usage: QUAY_USER=myuser make image-dev
QUAY_USER ?=
DEV_BASE_IMAGE ?= registry.access.redhat.com/ubi9/ubi-minimal:latest
DEV_TAG ?= dev-$(GIT_SHA)

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Code Generation

.PHONY: generate
generate: $(OAPI_CODEGEN) download ## Generate OpenAPI types using oapi-codegen
	@rm -rf pkg/api/openapi
	@mkdir -p pkg/api/openapi openapi
	@rm -f openapi/openapi.yaml
	@cp "$$($(GO) list -m -f '{{.Dir}}' github.com/openshift-hyperfleet/hyperfleet-api-spec)/schemas/$(VARIANT)/openapi.yaml" openapi/openapi.yaml
	$(OAPI_CODEGEN) --config openapi/oapi-codegen.yaml openapi/openapi.yaml
	@printf '// DO NOT REMOVE\npackage openapi\n' > pkg/api/openapi/stub.go

##@ Development

.PHONY: build
build: generate ## Build the sentinel binary
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) ./cmd/sentinel

.PHONY: install
install: ## Build and install binary to GOPATH/bin
	$(GO) install $(GOFLAGS) -ldflags "$(LDFLAGS)" ./cmd/sentinel

.PHONY: install-hooks
install-hooks: ## Install pre-commit hooks
	pre-commit install

.PHONY: run
run: build ## Run the sentinel service
	./$(BINARY_NAME) serve

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)
	rm -rf pkg/api/openapi
	rm -f coverage.out

##@ Testing

.PHONY: test
test: generate ## Run unit tests (default)
	$(GO) test -v -race -coverprofile=coverage.out ./...

.PHONY: test-unit
test-unit: generate ## Run unit tests only
	$(GO) test -v -race -cover ./internal/config/
	$(GO) test -v -race -cover ./internal/client/
	$(GO) test -v -race -cover ./internal/engine/
	$(GO) test -v -race -cover ./internal/health/
	$(GO) test -v -race -cover ./internal/publisher/
	$(GO) test -v -race -cover ./internal/sentinel/
	$(GO) test -v -race -cover ./pkg/...

.PHONY: test-integration
test-integration: generate ## Run integration tests only
	@echo "Running integration tests..."
	TESTCONTAINERS_RYUK_DISABLED=true $(GO) test -v -race -tags=integration ./test/integration/... -timeout 30m

.PHONY: test-all
test-all: test test-integration test-helm lint ## Run unit tests, integration tests, Helm tests, and lint

.PHONY: test-coverage
test-coverage: test ## Run tests and show coverage
	$(GO) tool cover -html=coverage.out

##@ Code Quality

.PHONY: fmt
fmt: ## Format Go code
	$(GOFMT) -s -w .

.PHONY: gofmt
gofmt: fmt ## Alias for fmt

.PHONY: fmt-check
fmt-check: ## Check if code is formatted
	@diff=$$($(GOFMT) -s -d .); \
	if [ -n "$$diff" ]; then \
		echo "Code is not formatted. Run 'make fmt' to fix:"; \
		echo "$$diff"; \
		exit 1; \
	fi

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: go-vet
go-vet: vet ## Alias for vet

.PHONY: lint
lint: $(GOLANGCI_LINT) ## Run golangci-lint
	$(GOLANGCI_LINT) run

.PHONY: verify
verify: fmt-check vet ## Run all verification checks

.PHONY: lint-check
lint-check: fmt-check vet ## Run static code analysis (alias for verify, follows architecture naming)

##@ Dependencies

.PHONY: tidy
tidy: ## Tidy go.mod
	$(GO) mod tidy

.PHONY: download
download: ## Download dependencies
	$(GO) mod download

##@ Helm Charts

HELM_CHART_DIR := charts

.PHONY: helm-docs
helm-docs: $(HELM_DOCS) ## Generate Helm chart README from values.yaml annotations
	$(HELM_DOCS) --chart-search-root=$(HELM_CHART_DIR) --sort-values-order=file

.PHONY: verify-helm-docs
verify-helm-docs: $(HELM_DOCS) ## Verify chart README is up to date
	$(HELM_DOCS) --chart-search-root=$(HELM_CHART_DIR) --sort-values-order=file
	@git diff --exit-code $(HELM_CHART_DIR)/README.md > /dev/null 2>&1 || \
		(echo "ERROR: $(HELM_CHART_DIR)/README.md is out of date. Run 'make helm-docs' and commit the result." && exit 1)

# kubeconform flags for validating rendered Helm templates against Kubernetes
# and CRD schemas. Uses the datreeio/CRDs-catalog for ServiceMonitor,
# PrometheusRule, and PodMonitoring schemas.
KUBECONFORM_FLAGS := \
	-strict \
	-kubernetes-version 1.30.0 \
	-schema-location default \
	-schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'

.PHONY: test-helm
test-helm: $(KUBECONFORM) verify-helm-docs ## Test Helm charts (lint, template, validate, kubeconform)
	@echo "Testing Helm charts..."
	@if ! command -v helm > /dev/null; then \
		echo "Error: helm not found. Please install Helm:"; \
		echo "  brew install helm  # macOS"; \
		echo "  curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash  # Linux"; \
		exit 1; \
	fi
	@echo "Linting Helm chart..."
	helm lint $(HELM_CHART_DIR)/ \
		--set image.registry=quay.io \
		--set image.repository=openshift-hyperfleet/hyperfleet-sentinel \
		--set image.tag=latest
	@echo ""
	@echo "Testing template rendering with default values..."
	helm template test-release $(HELM_CHART_DIR)/ \
		--set image.registry=quay.io \
		--set image.repository=openshift-hyperfleet/hyperfleet-sentinel \
		--set image.tag=latest | $(KUBECONFORM) $(KUBECONFORM_FLAGS)
	@echo "Default values template OK"
	@echo ""
	@echo "Testing template with custom image..."
	helm template test-release $(HELM_CHART_DIR)/ \
		--set image.registry=quay.io \
		--set image.repository=myorg/hyperfleet-sentinel \
		--set image.tag=v1.0.0 | $(KUBECONFORM) $(KUBECONFORM_FLAGS)
	@echo "Custom image config template OK"
	@echo ""
	@echo "Testing template with PDB enabled..."
	helm template test-release $(HELM_CHART_DIR)/ \
		--set image.registry=quay.io \
		--set image.repository=openshift-hyperfleet/hyperfleet-sentinel \
		--set image.tag=latest \
		--set podDisruptionBudget.enabled=true \
		--set podDisruptionBudget.maxUnavailable=1 | $(KUBECONFORM) $(KUBECONFORM_FLAGS)
	@echo "PDB config template OK"
	@echo ""
	@echo "Testing template with PDB disabled..."
	helm template test-release $(HELM_CHART_DIR)/ \
		--set image.registry=quay.io \
		--set image.repository=openshift-hyperfleet/hyperfleet-sentinel \
		--set image.tag=latest \
		--set podDisruptionBudget.enabled=false | $(KUBECONFORM) $(KUBECONFORM_FLAGS)
	@echo "PDB disabled template OK"
	@echo ""
	@echo "Testing template with RabbitMQ broker..."
	helm template test-release $(HELM_CHART_DIR)/ \
		--set image.registry=quay.io \
		--set image.repository=openshift-hyperfleet/hyperfleet-sentinel \
		--set image.tag=latest \
		--set broker.type=rabbitmq \
		--set broker.rabbitmq.url=amqp://user:pass@rabbitmq:5672/hyperfleet | $(KUBECONFORM) $(KUBECONFORM_FLAGS)
	@echo "RabbitMQ broker template OK"
	@echo ""
	@echo "Testing template with Google Pub/Sub broker..."
	helm template test-release $(HELM_CHART_DIR)/ \
		--set image.registry=quay.io \
		--set image.repository=openshift-hyperfleet/hyperfleet-sentinel \
		--set image.tag=latest \
		--set broker.type=googlepubsub \
		--set broker.googlepubsub.projectId=test-project | $(KUBECONFORM) $(KUBECONFORM_FLAGS)
	@echo "Google Pub/Sub broker template OK"
	@echo ""
	@echo "Testing template with PodMonitoring enabled..."
	helm template test-release $(HELM_CHART_DIR)/ \
		--set image.registry=quay.io \
		--set image.repository=openshift-hyperfleet/hyperfleet-sentinel \
		--set image.tag=latest \
		--set monitoring.podMonitoring.enabled=true \
		--set monitoring.podMonitoring.interval=15s | $(KUBECONFORM) $(KUBECONFORM_FLAGS)
	@echo "PodMonitoring config template OK"
	@echo ""
	@echo "Testing template with ServiceMonitor enabled..."
	helm template test-release $(HELM_CHART_DIR)/ \
		--set image.registry=quay.io \
		--set image.repository=openshift-hyperfleet/hyperfleet-sentinel \
		--set image.tag=latest \
		--set monitoring.serviceMonitor.enabled=true \
		--set monitoring.serviceMonitor.interval=30s | $(KUBECONFORM) $(KUBECONFORM_FLAGS)
	@echo "ServiceMonitor config template OK"
	@echo ""
	@echo "Testing template with PrometheusRule enabled..."
	helm template test-release $(HELM_CHART_DIR)/ \
		--set image.registry=quay.io \
		--set image.repository=openshift-hyperfleet/hyperfleet-sentinel \
		--set image.tag=latest \
		--set monitoring.prometheusRule.enabled=true | $(KUBECONFORM) $(KUBECONFORM_FLAGS)
	@echo "PrometheusRule config template OK"
	@echo ""
	@echo "Testing template with custom resource selector..."
	helm template test-release $(HELM_CHART_DIR)/ \
		--set image.registry=quay.io \
		--set image.repository=openshift-hyperfleet/hyperfleet-sentinel \
		--set image.tag=latest \
		--set config.resourceType=nodepools \
		--set config.pollInterval=10s | $(KUBECONFORM) $(KUBECONFORM_FLAGS)
	@echo "Custom resource selector template OK"
	@echo ""
	@echo "All Helm chart tests passed!"

##@ Container Images

.PHONY: check-container-tool
check-container-tool:
ifndef CONTAINER_TOOL
	@echo "Error: No container tool found (podman or docker)"
	@echo ""
	@echo "Please install one of:"
	@echo "  brew install podman   # macOS"
	@echo "  brew install docker   # macOS"
	@echo "  dnf install podman    # Fedora/RHEL"
	@exit 1
endif

.PHONY: image
image: check-container-tool ## Build container image with configurable registry/tag
	@echo "Building image $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) ..."
	$(CONTAINER_TOOL) build \
		--platform $(PLATFORM) \
		--build-arg GIT_SHA=$(GIT_SHA) \
		--build-arg GIT_DIRTY=$(GIT_DIRTY) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		--build-arg APP_VERSION=$(APP_VERSION) \
		-t $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) .
	@echo "Image built: $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)"

.PHONY: image-push
image-push: image ## Build and push container image
	@echo "Pushing image $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) ..."
	$(CONTAINER_TOOL) push $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
	@echo "Image pushed: $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)"

.PHONY: image-dev
image-dev: check-container-tool ## Build and push to personal Quay registry (requires QUAY_USER)
ifndef QUAY_USER
	@echo "Error: QUAY_USER is not set"
	@echo ""
	@echo "Usage: QUAY_USER=myuser make image-dev"
	@echo ""
	@echo "This will build and push to: quay.io/\$$QUAY_USER/$(IMAGE_NAME):$(DEV_TAG)"
	@exit 1
endif
	@echo "Building dev image quay.io/$(QUAY_USER)/$(IMAGE_NAME):$(DEV_TAG) ..."
	$(CONTAINER_TOOL) build \
		--platform $(PLATFORM) \
		--build-arg BASE_IMAGE=$(DEV_BASE_IMAGE) \
		--build-arg GIT_SHA=$(GIT_SHA) \
		--build-arg GIT_DIRTY=$(GIT_DIRTY) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		--build-arg APP_VERSION=0.0.0-dev \
		-t quay.io/$(QUAY_USER)/$(IMAGE_NAME):$(DEV_TAG) .
	@echo "Pushing dev image..."
	$(CONTAINER_TOOL) push quay.io/$(QUAY_USER)/$(IMAGE_NAME):$(DEV_TAG)
	@echo ""
	@echo "Dev image pushed: quay.io/$(QUAY_USER)/$(IMAGE_NAME):$(DEV_TAG)"
	@echo ""
	@echo "Add to your terraform.tfvars:"
	@echo "  sentinel_image_tag = \"$(DEV_TAG)\""

.PHONY: quay-login
quay-login:
	@if [ -z "$(CONTAINER_TOOL)" ]; then \
		echo "Error: neither podman nor docker found in PATH"; \
		exit 1; \
	fi
	$(CONTAINER_TOOL) login quay.io
