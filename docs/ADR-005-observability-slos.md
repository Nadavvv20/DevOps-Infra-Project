# ADR-005 — Observability Strategy & SLOs

## Status
Accepted

## Context

An ecommerce platform requires comprehensive observability across three pillars: **Metrics** (what is happening), **Logs** (why it happened), and **Traces** (where in the call chain it happened). We need to define Service Level Objectives (SLOs) that translate business requirements into measurable engineering targets, and implement alerting that fires on SLO burn rate rather than raw thresholds.

---

## SLO Definitions

### Online Boutique SLOs

| SLO | Target | Measurement Window | Alert Threshold |
|---|---|---|---|
| Frontend Availability | 99.9% uptime (8.76 hours/year downtime) | 30-day rolling | < 99.5% over 1h |
| Frontend Latency p99 | < 500ms | 5-minute windows | > 500ms for 5m |
| Checkout Success Rate | 99.5% | 30-day rolling | Error rate > 1% for 2m |
| Cart Read Latency p99 | < 50ms | 5-minute windows | > 50ms for 5m |
| Image Build Pipeline | < 10 min (p95 build+scan) | Per-run | > 15 min |

### Error Budget Calculation

For **99.9% availability** over 30 days:  
- Total minutes in 30 days = 43,200  
- Allowed downtime = 43,200 × 0.001 = **43.2 minutes/month**

An error budget burn rate alert fires when:
- Burn rate > 14× over 1 hour → consume 5% of monthly budget → **Page immediately**
- Burn rate > 6× over 6 hours → consume 10% of monthly budget → **Ticket + Slack**

---

## Metrics Layer: Prometheus + Grafana

### Collection Architecture

```
┌───────────────────────────────────────────────────────────────────┐
│  EKS Cluster                                                       │
│                                                                    │
│  ┌─────────────┐   scrape (15s)   ┌─────────────────────────┐    │
│  │ ServiceMonitor│───────────────▶│ Prometheus StatefulSet   │    │
│  │ (CRD)       │                  │ (50 GiB gp3, 30d retention)│  │
│  └─────────────┘                  └──────────┬──────────────┘    │
│                                               │ remote_write      │
│  ┌─────────────┐                             ▼                    │
│  │ PodMonitor  │                  ┌─────────────────────────┐    │
│  │ (CRD)       │                  │ Amazon Managed Prometheus│    │
│  └─────────────┘                  │ (long-term storage)      │    │
│                                   └──────────────────────────┘   │
│  ┌─────────────┐                             │                    │
│  │AlertManager │◀── PrometheusRule alerts ──┘                    │
│  └──────┬──────┘                                                  │
│         │                                                          │
│         ▼                                                          │
│  ┌─────────────┐      ┌──────────────┐                           │
│  │  PagerDuty  │      │  Slack       │                           │
│  └─────────────┘      └──────────────┘                           │
└───────────────────────────────────────────────────────────────────┘
```

### Key Metric Sources

| Exporter | Metrics Provided |
|---|---|
| `kube-state-metrics` | Deployment replica counts, pod phase, PVC status |
| `node-exporter` (DaemonSet) | Node CPU, memory, disk, network from `/proc` |
| `cadvisor` (built into kubelet) | Per-container CPU/memory usage |
| `karpenter` (built-in) | Node provisioning time, interruptions, consolidation events |
| `aws-load-balancer-controller` | ALB target health, request count |
| `Online Boutique apps` | Custom gRPC/HTTP counters (via Prometheus SDK) |

### Critical Prometheus Queries (for interview)

**Frontend error rate:**
```promql
sum(rate(http_requests_total{job="frontend", status=~"5.."}[5m]))
/
sum(rate(http_requests_total{job="frontend"}[5m]))
```

**p99 request latency:**
```promql
histogram_quantile(0.99,
  sum(rate(http_request_duration_seconds_bucket{job="frontend"}[5m])) by (le)
)
```

**Karpenter pending pod wait time:**
```promql
karpenter_pods_startup_time_seconds{quantile="0.99"}
```

**Node memory pressure:**
```promql
(node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes) < 0.1
```

**Aurora replica lag:**
```promql
aws_rds_replica_lag_average{DBClusterIdentifier=~"online-boutique.*"}
```

---

## Grafana Dashboard Strategy

### Pre-built Dashboards (imported by ID)

| Dashboard | Grafana ID | Purpose |
|---|---|---|
| Kubernetes Cluster Overview | 7249 | Node health, pod counts, resource usage |
| Karpenter | 20398 | Provisioning speed, interruptions, utilization |
| Kubernetes Deployment | 8588 | Per-deployment CPU/memory/replicas |
| Node Exporter Full | 1860 | Deep node-level metrics |
| ArgoCD | 19147 | Sync status, application health |

### Custom Dashboard: Online Boutique SLOs

Built in-house (as ConfigMap for GitOps provisioning):
- Error budget burn rate (1h and 6h windows)
- Frontend p50/p95/p99 latency heatmap
- Checkout funnel: cart → checkout → payment success rate
- Cart service Redis connection pool utilization
- Aurora read replica lag and connection count

---

## CloudWatch Container Insights

CloudWatch Container Insights is enabled via the CloudWatch Agent DaemonSet (deployed via Helm, IRSA-enabled). It provides:

| Metric Type | Retention | Cost |
|---|---|---|
| Container-level CPU/mem | 15 months | $0.30/metric/month |
| Application logs | Configurable (7-90 days) | $0.50/GB ingested |
| Cluster-level summary | 15 months | Free |

**Why CloudWatch alongside Prometheus?**

| Use Case | Prometheus/Grafana | CloudWatch |
|---|---|---|
| Application SLO dashboards | ✅ Best — custom PromQL queries | ❌ Limited query flexibility |
| AWS service metrics (RDS, ElastiCache) | ❌ Requires extra exporter | ✅ Native — no extra tooling |
| Log analytics | ❌ Not a log tool | ✅ CloudWatch Logs Insights |
| Cross-account visibility | ❌ Complex | ✅ Native via Organizations |
| Cost | Free (self-hosted) | Pay-per-metric |

**Decision:** Run both. Prometheus/Grafana for SLO-grade application metrics and alerting. CloudWatch for AWS-native service health (RDS, ElastiCache, ALB) and log aggregation. Use CloudWatch Metric Streams to forward selected CW metrics into Prometheus for unified dashboards.

---

## Log Aggregation

### Fluent Bit DaemonSet

Fluent Bit runs as a DaemonSet on every node, tailing container log files from `/var/log/containers/`. It routes:

```
Pod stdout/stderr
      │
      ▼
Fluent Bit (per node)
      │
      ├── CloudWatch Logs (/aws/eks/online-boutique/application)
      └── S3 (long-term archive, via Firehose)
```

**Structured logging requirement:** All Online Boutique services emit JSON-formatted logs. Fluent Bit parses JSON and indexes `severity`, `service`, `traceID`, `spanID` fields — enabling CloudWatch Logs Insights queries like:

```sql
fields @timestamp, service, severity, message
| filter severity = "ERROR"
| sort @timestamp desc
| limit 50
```

---

## Distributed Tracing

The Online Boutique uses gRPC between services. All services already instrument OpenTelemetry trace context propagation. We deploy the **OpenTelemetry Collector** (as a Deployment + DaemonSet sidecar) to:

1. Receive traces from services (OTLP/gRPC on port 4317)
2. Export to **AWS X-Ray** (via OTLP → X-Ray exporter)

This provides end-to-end trace visualization: a single HTTP request to `/checkout` is traced through frontend → checkoutservice → paymentservice → emailservice, with per-span latency breakdown.

---

## Alerting Runbook Structure

Each PrometheusRule alert links to a runbook URL:

```yaml
annotations:
  runbook: "https://wiki.example.com/runbooks/frontend-high-error-rate"
  summary: "Frontend error rate exceeds SLO"
```

**Runbook template for `FrontendHighErrorRate`:**
1. Check `kubectl get pods -n online-boutique` — are pods healthy?
2. Check recent ArgoCD sync — did a bad deploy just land?
3. `kubectl logs -l app=frontend -n online-boutique --tail=100` — error pattern?
4. Check ALB target health in AWS Console
5. Check Aurora connection count — is the DB accepting connections?
6. If deploy is the cause: `argocd app rollback online-boutique`

---

## Health Check Design

### Kubernetes Probes

```yaml
livenessProbe:
  httpGet:
    path: /_healthz
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 10
  failureThreshold: 3

readinessProbe:
  httpGet:
    path: /_healthz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5
  failureThreshold: 2

startupProbe:
  httpGet:
    path: /_healthz
    port: 8080
  failureThreshold: 30
  periodSeconds: 10
```

**Why three separate probes?**

- `livenessProbe`: Kubernetes restarts the pod if the app is deadlocked (failing liveness). Too-aggressive settings cause restart loops.
- `readinessProbe`: Kubernetes removes the pod from Service endpoints if it can't serve traffic (e.g., during warmup or DB connection loss). Does **not** restart the pod — it just stops sending traffic.
- `startupProbe`: Gives slow-starting JVM services (adservice, Java) enough time to initialize before liveness kicks in.

> [!TIP]
> A common interview gotcha: if `livenessProbe` uses the same `/_healthz` as `readinessProbe` but fires too quickly, a slow-starting pod gets killed before it finishes starting up. The `startupProbe` prevents this by disabling liveness/readiness checks until startup succeeds.
