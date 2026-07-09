# Sentinel Profiling Results

> **Audience:** Platform operators and SREs sizing Sentinel deployments.

[HYPERFLEET-556](https://issues.redhat.com/browse/HYPERFLEET-556) — Validate resource limits and requests against actual Sentinel consumption.

## Goal

Validate that current resource defaults are appropriately sized under realistic load. Current defaults: CPU 100m request / 500m limit, memory 128Mi request / 512Mi limit. Ensure limits prevent noisy-neighbor issues without causing OOM kills or CPU throttling.

## Setup

- **Poll interval:** 5s (chart default)
- **Duration:** 15 min per scenario
- **Sample interval:** 60s (`kubectl top`)
- **Tracing:** Disabled
- **Mock API:** In-cluster mock-hyperfleet-api
- **Cluster counts:** 100, 1000, 5000 (number of cluster resources returned by the API per poll)
- **Tooling:** [`test/profiling/README.md`](../test/profiling/README.md) — `profile.sh` to capture samples, `analyze.py` to aggregate

## Results

| Clusters | CPU                 | Memory               |
| -------- | ------------------- | -------------------- |
| 100      | 43–63m (avg 48m)    | 11–13Mi (avg 12Mi)   |
| 1000     | 84–106m (avg 96m)   | 25–40Mi (avg 28Mi)   |
| 5000     | 80–172m (avg 101m)  | 84–147Mi (avg 96Mi)  |

## Findings

- CPU stays well under the current 500m limit at all scales. Peak was 172m at 5000 clusters. No risk of CPU throttling.
- Memory grows with cluster count. The 5000-cluster run ranges between ~84Mi and ~147Mi. Peak 147Mi is within the current 512Mi limit with healthy headroom.
- CPU requests (currently 100m) are tight at 1000 clusters where the average is 96m and peaks hit 106m. Over-provisioned for 100 clusters (avg 48m) but reasonable as a default.
- Memory requests (currently 128Mi) are appropriate up to ~1000 clusters. At 5000 clusters the average is 96Mi but peaks reach 147Mi, exceeding 128Mi.
- At 5000 clusters with a 5s poll interval, cycle time exceeds the interval, so Sentinel runs back-to-back with no idle time.

## Recommendations

Current defaults (CPU 100m/500m, memory 128Mi/512Mi) support up to ~1000 clusters per Sentinel instance. At this scale the CPU request (100m) is tight with peaks exceeding it, but the 500m limit provides sufficient burst headroom. Beyond 1000 clusters, operators should increase resource requests and limits or shard across multiple instances using `resourceSelector`. The results table above can be used to guide sizing.

A Vertical Pod Autoscaler (VPA) could automatically adjust requests to match actual usage, removing the need to manually tune. Most useful if cluster count drifts over time.

## See Also

- [Metrics Documentation](metrics.md) - Resource consumption metrics
- [Multi-Instance Deployment](multi-instance-deployment.md) - How to shard across multiple Sentinel instances
- [Runbook](runbook.md) - Operational guidance and troubleshooting
- [Profiling Tooling](../test/profiling/README.md) - How to run your own profiles
