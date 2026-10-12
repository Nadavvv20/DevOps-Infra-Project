# ADR-002 — Data Tier Architecture Decision Record

## Status
Accepted

## Context

The Online Boutique has two primary data storage requirements:

1. **Transactional / relational data** — order history, product catalog enrichment, user profiles. Requires ACID guarantees, structured queries, foreign key integrity.
2. **Session / cart state** — millisecond-latency reads/writes for the active shopping cart. Cart data is ephemeral (session-scoped), schema-free, and must survive pod restarts.

---

## Decision 1: Relational Database — Aurora PostgreSQL vs RDS PostgreSQL

### Workload Profile

| Attribute | Value |
|---|---|
| Pattern | OLTP — small, frequent transactions |
| Peak TPS | ~500 (mid-scale ecommerce) |
| Avg row size | 1–4 KB |
| Backup RPO | < 1 hour |
| RTO | < 5 minutes |
| Read:Write ratio | ~80:20 |

### Option A: Amazon RDS PostgreSQL (Multi-AZ)

A single PostgreSQL instance with a standby replica in another AZ using synchronous replication. Failover promotes the standby in ~60-120 seconds.

**Pros:**
- Simpler pricing model (one instance price)
- Familiar single-instance operational model
- Suitable for < 100 GB workloads

**Cons:**
- Standby is not readable — the replica sits idle 100% of the time, burning 100% of instance cost
- Storage scales manually (requires modify + reboot window)
- Failover takes 60-120s (DNS propagation delay)
- No serverless autoscaling option

**Cost estimate (db.r6g.large, Multi-AZ, us-east-1):** ~$340/month

### Option B: Aurora PostgreSQL (Chosen)

Aurora decouples storage from compute. The storage layer is a distributed, SSD-backed, 6-copy quorum system spread across 3 AZs. Compute instances (writer + readers) connect to this shared volume.

**Pros:**
- **Storage auto-scaling:** grows in 10 GB increments up to 128 TiB with zero downtime
- **Readers are live:** reader endpoint serves read traffic, so you get value from the second instance dollar
- **Faster failover:** ~30 seconds (no DNS re-propagation needed — reader promotes instantly)
- **Aurora I/O Optimized:** at high I/O, flat monthly I/O cost is cheaper than per-request pricing (break-even at ~25% I/O utilization)
- **RDS Data API:** HTTP-based query interface eliminates connection pool management for serverless workloads

**Cons:**
- ~20% premium per vCPU-hour vs RDS
- Aurora storage has a minimum cost even when empty (~$0.10/GB-month)
- Aurora versions lag behind upstream PostgreSQL releases by ~6 months

**Cost estimate (db.r7g.large writer + db.t4g.medium reader, us-east-1):** ~$380/month

### Decision: Aurora PostgreSQL ✅

The faster failover RTO (30s vs 120s), live read scaling, and storage auto-scaling justify the ~12% premium. For an ecommerce workload where a 2-minute checkout outage directly converts to lost revenue, the Aurora RTO advantage is a hard requirement.

**Version selected:** Aurora PostgreSQL 15 (LTS-aligned, supports `pg_vector` for future ML recommendation features)

**Instance selection:**
- **Dev:** `db.t4g.medium` (burstable, cost-effective for low traffic)
- **Prod writer:** `db.r7g.large` (ARM Graviton3, ~20% better price/performance than x86 r6g)
- **Prod reader:** `db.t4g.medium` (handles ~80% read traffic, auto-promoted on writer failure)

---

## Decision 2: Cache — ElastiCache Redis vs In-Cluster Redis StatefulSet

### Workload Profile (Cart Service)

| Attribute | Value |
|---|---|
| Access pattern | GET/SET on cart key per user session |
| TTL | 30 days (cart expires) |
| Data size | < 10 KB per user |
| Target latency | < 5ms p99 |
| Durability | Best-effort (cart loss is recoverable) |

### Option A: In-Cluster Redis (StatefulSet + PVC)

Run Redis as a Kubernetes StatefulSet with a `gp3` EBS PVC. The Online Boutique ships with this pattern by default.

**Pros:**
- Zero extra AWS cost (runs on Karpenter-managed EC2)
- Same cluster lifecycle — simpler for dev

**Cons:**
- You own Redis operational complexity: upgrades, AOF/RDB config, replication setup
- EBS PVC binds Redis to a single AZ — AZ failure = cart data loss
- EBS IO2/gp3 throughput limits apply; Redis on NVMe is far faster
- Single point of failure unless you add Redis Sentinel or Cluster (adds more ops burden)
- No free Prometheus metrics export without `redis-exporter` sidecar

**Verdict:** Acceptable for dev. Unacceptable for prod.

### Option B: ElastiCache Redis (Chosen for prod)

AWS-managed Redis with automatic failover, Multi-AZ replication, and daily automated backups.

**Pros:**
- Zero operational overhead — AWS handles patches, failover, and engine upgrades
- Multi-AZ: primary in AZ-1, replica in AZ-2 — automatic promotion in ~30 seconds
- TLS transit encryption + AUTH token natively supported
- CloudWatch metrics (engine CPU, evictions, connections) without extra tooling
- Redis 7.x: supports Redis Functions, better ACL granularity

**Cons:**
- Additional AWS cost ($40-120/month depending on node type)
- Cannot run custom Redis modules (e.g., RedisGraph)

**Cost estimate:**
- Dev: `cache.t4g.small` × 1 = ~$20/month
- Prod: `cache.r7g.large` × 2 (Multi-AZ) = ~$230/month

### Decision: ElastiCache Redis ✅

For production, the operational simplicity and Multi-AZ resilience are non-negotiable. Dev uses in-cluster Redis (or ElastiCache t4g.small) to match the `REDIS_URL` environment variable contract — no code change required.

---

## Data Tier Architecture Diagram

```
┌─────────────────────────────────────────────────────────┐
│                    EKS Cluster                          │
│                                                         │
│  ┌──────────────┐    gRPC    ┌──────────────────────┐   │
│  │ checkoutservice │───────▶│  Aurora PostgreSQL    │   │
│  │ (Go)         │           │  Writer (us-east-1a)  │   │
│  └──────────────┘           └──────────────────────┘   │
│                                        │ sync replicate  │
│  ┌──────────────┐    gRPC              ▼                 │
│  │  cartservice │    ┌──────────────────────┐           │
│  │  (C#)        │───▶│  ElastiCache Redis   │           │
│  └──────────────┘    │  Primary (AZ-1)      │           │
│                       │  Replica  (AZ-2)     │           │
│                       └──────────────────────┘           │
└─────────────────────────────────────────────────────────┘
```

---

## Rejected Options

| Option | Reason Rejected |
|---|---|
| DynamoDB for cart | Overkill for sub-millisecond simple key-value; adds IAM complexity, no TTL per-item in free tier |
| Aurora Serverless v2 for prod | ACU cold-start latency spikes under sudden traffic; better for dev or very spiky workloads |
| MongoDB Atlas | Third-party vendor; adds egress cost, VPC peering complexity, additional secret management surface |
| Memcached | No persistence, no TLS AUTH, no replication — unsuitable for session data |
