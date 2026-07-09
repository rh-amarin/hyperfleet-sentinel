# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- CEL decision and payload contexts now expose `name`, `spec`, and `references` fields from generic resources

### Changed
- OpenAPI schema is now sourced from the versioned `hyperfleet-api-spec` Go module instead of being downloaded from `hyperfleet-api` main branch
- Documented single-instance deployment limitation — running multiple replicas with overlapping resource selectors causes duplicate events. Added recommended deployment configuration and scaling guidance
- `resource_type` config accepts any registered entity type plural (e.g. `wifconfigs`), no longer limited to `clusters` and `nodepools`
- API client replaced typed per-entity endpoints with generic `GET /api/hyperfleet/v1/{plural}` resource list endpoint (`hyperfleet-api-spec` v1.0.25)

### Deprecated

### Removed
- BREAKING CHANGE: `messaging_system` config field and `MESSAGING_SYSTEM` env var removed, `messaging.system` OTel span attribute is now derived from `publisher.BrokerType()`. Remove `messaging_system` from configs before upgrading.

### Fixed
- Sentinel now paginates API responses, fetching all resources instead of only the first page (default size 20). Environments with more than 20 clusters/nodepools were missing reconciliation events.

### Security

## [0.1.1](https://github.com/openshift-hyperfleet/hyperfleet-sentinel/compare/v0.1.0...v0.1.1) - 2026-03-10

### Added
- Standard metrics labels to Sentinel Prometheus metrics for consistent monitoring across HyperFleet components
- ServiceMonitor resource for Prometheus Operator environments
- PodDisruptionBudget to protect Sentinel availability during voluntary disruptions
- Helm chart linting and template validation to CI via Makefile targets
- Support for nested field paths in `message_data` configuration for richer event content
- Functional health and readiness probes beyond basic liveness checks

### Changed
- Updated `hyperfleet-broker` to v1.1.0 and integrated `MetricsRecorder` for broker-level observability
- Standardized Helm value structure for consistency across HyperFleet charts
- Moved Sentinel Helm chart to `charts/` directory following repository conventions
- GCP-specific monitoring resources are now disabled by default
- Standardized Dockerfile and Makefile for unified image build process
- Standardized version injection to avoid collisions with `go-toolset` environment variables

### Fixed
- RabbitMQ connection URL now included in broker ConfigMap for proper broker discovery
- CA certificates copied from builder stage to `ubi9-micro` runtime, resolving TLS verification failures
- Clarified Helm deployment instructions for GKE environments using Quay images

## [0.1.0](https://github.com/openshift-hyperfleet/hyperfleet-sentinel/compare/v0.0.0...v0.1.0) - 2026-02-19

### Added
- Initial release of HyperFleet Sentinel Service
- Kubernetes resource polling for clusters and nodepools
- CloudEvents publishing with broker abstraction (GCP Pub/Sub, RabbitMQ, Stub)
- Horizontal sharding via resource selector labels
- Configurable polling intervals and max age intervals (not ready vs ready resources)
- CEL-based message data templating for custom CloudEvents payloads
- Prometheus metrics for observability
- Grafana dashboard for monitoring
- PodMonitoring support for GKE with Google Cloud Managed Prometheus
- Helm chart for deployment
- Integration tests with testcontainers (RabbitMQ and GCP Pub/Sub)
- OpenAPI client generation from hyperfleet-api specification
- Configuration validation at startup
- HyperFleet API client with retry logic
- Comprehensive test coverage and linting

---

<!-- Changelog Guidelines:

Follow these guidelines when updating the changelog:

1. **What to include:**
   - All notable changes that affect users
   - New features, bug fixes, security fixes
   - Breaking changes (mark with "BREAKING CHANGE" in description)
   - Deprecations and removals

2. **What NOT to include:**
   - Internal refactoring that doesn't affect users, i.e. editorial/layout fixes only; component boundary or interface changes MUST be logged as they impact E2E testing
   - Development tooling changes
   - Documentation typo fixes
   - Code formatting changes

3. **How to categorize changes:**
   - **Added** for new features
   - **Changed** for changes in existing functionality
   - **Deprecated** for soon-to-be removed features
   - **Removed** for now removed features
   - **Fixed** for any bug fixes
   - **Security** for vulnerability fixes

4. **Version format:**
   - Use semantic versioning (MAJOR.MINOR.PATCH)
   - Include release date in YYYY-MM-DD format
   - Link to release tags if available

5. **Entry format:**
   ```markdown
   ### Added
   - Brief description of the change
   - Another change with [link to issue/PR](URL) if relevant
   ```

6. **Example entries:**
   ```markdown
   ### Added
   - New API endpoint for cluster status monitoring
   - Support for custom authentication providers

   ### Changed
   - BREAKING CHANGE: Updated API response format for cluster resources
   - Improved error handling for network timeouts

   ### Fixed
   - Fixed memory leak in status polling service
   - Resolved authentication timeout issues
   ```
-->
