# ADR-003 — Kubernetes Workload & State Design

## Status
Accepted

## Context

The Online Boutique consists of 11 microservices with different statefulness, persistence, identity, and network requirements. Choosing the wrong Kubernetes workload primitive is a common source of operational pain — a StatefulSet for a stateless service adds needless complexity; a Deployment for a stateful service risks data corruption on rolling updates.

---

## Kubernetes Workload Primitives Overview

| Primitive | Use When |
|---|---|
| `Deployment` | Stateless service; any pod is interchangeable; rolling replace is safe |
| `StatefulSet` | Stateful; each pod needs stable network identity and/or persistent storage |
| `DaemonSet` | One pod per node; infrastructure-level concerns (logging, monitoring agents) |
| `Job` / `CronJob` | Finite-completion tasks or scheduled batch work |

---

## Online Boutique Service Classification

### Stateless Services → `Deployment`

All of the following services are **functionally stateless** — they hold no data across requests that isn't stored in an external system (Redis, Aurora, or gRPC upstream). Any pod can serve any request.

| Service | Language | Why Stateless |
|---|---|---|
| `frontend` | Go | HTTP server; reads product catalog + cart per request; no local state |
| `productcatalogservice` | Go | Reads static JSON from disk at startup; all pods serve identical data |
| `currencyservice` | Node.js | Pure function: converts currency via forex rates fetched at startup |
| `paymentservice` | Node.js | Stateless; validates and mocks card charge; no local persistence |
| `emailservice` | Python | Fire-and-forget email; no local queue state |
| `checkoutservice` | Go | Orchestrates other gRPC calls; fan-out/gather; no local state |
| `recommendationservice` | Python | In-memory ML model loaded at startup; no per-request state |
| `shippingservice` | Go | Rate calculation from a static config; pure function |
| `adservice` | Java | Serves ads from in-memory map; loaded at startup |
| `loadgenerator` | Python | Locust traffic generator; ephemeral by nature |

**Deployment strategy for stateless services:**
```yaml
strategy:
  type: RollingUpdate
  rollingUpdate:
    maxSurge: 1
    maxUnavailable: 0   # Zero-downtime: bring new pod up before killing old one
```

**Why `maxUnavailable: 0`?** For ecommerce, a temporary capacity over-provision during deploy is acceptable. Having zero available replicas during a bad deploy is not.

### Stateful Services → `StatefulSet` or Managed AWS Service

#### `cartservice` (C#)

The cart service connects to Redis and is itself **stateless** — it stores cart state externally in Redis. However, if you run Redis in-cluster (dev mode), Redis must be a StatefulSet.

**In-cluster Redis (dev) → `StatefulSet`**

```yaml
kind: StatefulSet
spec:
  serviceName: "redis-headless"    # Stable DNS: redis-0.redis-headless.online-boutique.svc
  volumeClaimTemplates:
    - metadata: { name: redis-data }
      spec:
        storageClassName: gp3
        resources:
          requests:
            storage: 8Gi
```

**Why StatefulSet for Redis?**
1. **Stable network identity:** Redis replicas use pod-ordinal DNS (`redis-0`, `redis-1`). Sentinel and Redis Cluster require stable addresses for quorum negotiation. A Deployment assigns random pod names — breaking replication.
2. **Ordered startup/shutdown:** `redis-0` starts first, elected primary. `redis-1` then replicates. Deployment provides no ordering guarantees.
3. **PVC per-pod:** Each Redis pod gets its own PVC (`redis-data-redis-0`). If the pod restarts, it reattaches the same volume — no data loss. With a Deployment, a rescheduled pod might mount a different PVC.

**Production Redis → ElastiCache (eliminates StatefulSet entirely)**

By using ElastiCache, we offload all of this complexity. The `cartservice` Deployment simply reads `REDIS_URL` from the injected Secret.

### Infrastructure-Level Workloads → `DaemonSet`

| DaemonSet | Namespace | Purpose |
|---|---|---|
| `aws-node` (VPC CNI) | `kube-system` | One per node; assigns pod IPs from node ENI |
| `kube-proxy` | `kube-system` | One per node; maintains iptables/ipvs rules for Service routing |
| `cloudwatch-agent` | `amazon-cloudwatch` | One per node; ships Container Insights metrics to CloudWatch |
| `fluent-bit` (optional) | `kube-system` | One per node; ships pod logs to CloudWatch Logs / S3 |

**Why DaemonSet for CloudWatch Agent?**

Container Insights requires per-node metrics (CPU, memory, disk, network) from the kubelet stats API and `/proc`. These metrics are node-scoped — a single centralized deployment would need privileged host access to every node. A DaemonSet naturally runs on each node, requiring only node-level host mounts.

---

## Persistence Design

### Prometheus → StatefulSet (via kube-prometheus-stack)

Prometheus is a stateful time-series database. The kube-prometheus-stack Helm chart deploys it as a StatefulSet with:
- `volumeClaimTemplate` per pod for TSDB storage (50 GiB gp3 in our values)
- Stable pod name `prometheus-kube-prometheus-stack-prometheus-0` for alerting rule leader election
- 30-day retention (`--storage.tsdb.retention.time=30d`)

### Grafana → Deployment (with PVC)

Grafana is technically stateless if dashboards are managed via ConfigMaps/GitOps. We configure it with a PVC (`10Gi gp3`) for the SQLite database that stores user preferences and dashboard state. Since only one Grafana replica is needed, a Deployment with a single PVC is appropriate (no need for StatefulSet's ordinal-pod guarantees).

### AlertManager → StatefulSet

AlertManager uses a gossip cluster for HA deduplication. Stable pod DNS names are required for peer discovery — hence StatefulSet.

---

## Pod Disruption Budgets

| Service | PDB | minAvailable |
|---|---|---|
| `frontend` | Yes (prod) | 2 of 3 |
| `checkoutservice` | Yes (prod) | 1 of 2 |
| `cartservice` | Yes (prod) | 1 of 2 |
| Others | No | N/A |

PDBs prevent Karpenter consolidation or `kubectl drain` from simultaneously evicting all replicas of revenue-critical services.

---

## Summary Decision Table

| Service | Workload Type | Replicas (prod) | PDB | External State |
|---|---|---|---|---|
| frontend | Deployment | 3 | Yes | — |
| cartservice | Deployment | 2 | Yes | ElastiCache Redis |
| productcatalogservice | Deployment | 2 | No | — |
| currencyservice | Deployment | 2 | No | — |
| paymentservice | Deployment | 2 | No | — |
| checkoutservice | Deployment | 2 | Yes | — |
| emailservice | Deployment | 1 | No | — |
| recommendationservice | Deployment | 2 | No | — |
| shippingservice | Deployment | 2 | No | — |
| adservice | Deployment | 2 | No | — |
| redis (dev only) | StatefulSet | 1 | No | EBS gp3 PVC |
| prometheus | StatefulSet | 1 | No | EBS gp3 50GiB |
| alertmanager | StatefulSet | 2 | Yes | EBS gp3 5GiB |
| grafana | Deployment | 1 | No | EBS gp3 10GiB |
| cloudwatch-agent | DaemonSet | 1/node | N/A | Node host mounts |
