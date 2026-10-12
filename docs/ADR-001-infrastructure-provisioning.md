# ADR-001 — Infrastructure & Provisioning Strategy

## Status
Accepted

## Context

We need a repeatable, auditable, and environment-segregated IaC strategy to provision AWS infrastructure for the Online Boutique ecommerce platform. The strategy must support multiple environments (dev, prod), safe concurrent team workflows, automated CI/CD integration, and clear cost boundaries between environments.

---

## Decision: Directory-per-Environment over Terraform Workspaces

### Option A: Terraform Workspaces (Rejected)

Workspaces store all environment state in the same backend path prefix. A `terraform workspace select prod` mistake followed by `terraform destroy` can cascade across the wrong environment. Variable overriding is awkward — you either use `lookup(map, terraform.workspace)` which is hard to read, or you maintain separate `.tfvars` files anyway. Workspaces also share the same provider configuration, preventing per-environment AWS account isolation.

**Verdict:** Workspaces solve the wrong problem. They are suitable for short-lived ephemeral feature branches, not long-lived prod/dev segregation.

### Option B: Directory-per-Environment with Shared Modules (Chosen)

```
terraform/
├── modules/          # Reusable, versioned building blocks
│   ├── vpc/
│   ├── eks/
│   ├── karpenter/
│   ├── rds-aurora/
│   ├── elasticache/
│   ├── ecr/
│   ├── iam-roles/
│   └── helm-releases/
├── environments/
│   ├── dev/          # Isolated state, isolated AWS account/role
│   │   ├── main.tf
│   │   ├── variables.tf
│   │   └── terraform.tfvars
│   └── prod/
│       ├── main.tf
│       ├── variables.tf
│       └── terraform.tfvars
```

Each environment directory has:
- Its own S3 backend key (`dev/terraform.tfstate`, `prod/terraform.tfstate`)
- Its own AWS IAM role assumed via OIDC (GitHub Actions or CI runner)
- Its own `terraform.tfvars` controlling cost/HA knobs

**Why this wins:**
- Blast radius is physically isolated — a `terraform destroy` in `dev/` cannot touch `prod/`
- Per-environment AWS accounts or IAM roles are straightforward
- No magic variable interpolation on `terraform.workspace` — reading the code is obvious
- Modules are the DRY layer; environments are the composition layer

---

## Remote State Configuration

```hcl
# S3 backend with DynamoDB locking
backend "s3" {
  bucket         = "my-terraform-state-prod"
  key            = "prod/terraform.tfstate"
  region         = "il-central-1"
  dynamodb_table = "terraform-state-lock"
  encrypt        = true
}
```

**S3 Versioning** is enabled on the state bucket so any corrupted state can be rolled back.  
**DynamoDB state locking** prevents two `terraform apply` runs from racing and corrupting state.  
**KMS encryption** protects secrets embedded in state (passwords, tokens) at rest.

> [!CAUTION]
> State contains plaintext secret values (e.g., generated passwords before Secrets Manager). Never commit state to Git. The S3 bucket must have Block Public Access enabled and an S3 bucket policy denying non-SSL access.

---

## Autoscaling Design

### Control Plane (EKS Managed Node Groups)

| Node Group | Purpose | Instance Type | Min | Max |
|---|---|---|---|---|
| `system` | CoreDNS, kube-proxy, VPC CNI, Karpenter controller | `m5.large` | 2 | 4 |

The system node group is **tainted** (`CriticalAddonsOnly=true:NoSchedule`) so only system workloads tolerate it. Application pods never land here. This protects cluster stability from application pod disruptions.

### Application Tier (Karpenter)

Karpenter replaces the Cluster Autoscaler (CA) for all application workloads.

| Capability | Cluster Autoscaler | Karpenter |
|---|---|---|
| Provisioning speed | ~3-4 min (ASG cooldown) | ~30-60 seconds |
| Node rightsizing | No — fixed ASG launch template | Yes — bin-packs per pending pod |
| Spot interruption | Manual via ASG lifecycle hooks | Native SQS interruption queue |
| Consolidation | No | Yes — `WhenEmptyOrUnderutilized` |
| Multi-architecture | Requires separate ASGs | Single NodePool, per-pod arch |

**Karpenter NodePool configuration:**
```yaml
requirements:
  - key: "karpenter.sh/capacity-type"
    operator: In
    values: ["spot", "on-demand"]   # prefers Spot; falls back to OD
  - key: "karpenter.k8s.aws/instance-category"
    operator: In
    values: ["c", "m", "r"]        # compute, general, memory — avoids burstable t3
```

**Cost impact:** With Spot pricing at ~70% discount and Karpenter consolidation, estimated compute savings vs. fixed managed node groups: **35-55%** on typical ecommerce traffic patterns.

### VPC IP Prefix Delegation

```hcl
# vpc-cni addon configuration
ENABLE_PREFIX_DELEGATION = "true"
WARM_PREFIX_TARGET       = "1"
```

Standard VPC CNI allocates one `/32` IP per pod from the node's ENIs. A `m5.large` has max 30 IPs. With prefix delegation, each ENI attachment gets a `/28` (16 IPs), raising pod density to **290 pods per m5.large**. This dramatically reduces the number of nodes needed and cuts EC2 cost.

---

## Environment Differentiation Matrix

| Resource | Dev | Prod |
|---|---|---|
| NAT Gateways | 1 (single AZ) | 3 (one per AZ) |
| Aurora instances | 1 writer | 1 writer + 1 reader |
| Aurora class | `db.t4g.medium` | `db.r7g.large` |
| Redis nodes | 1 (no Multi-AZ) | 2 (Multi-AZ failover) |
| Redis class | `cache.t4g.small` | `cache.r7g.large` |
| Deletion protection | false | true |
| Skip final snapshot | true | false |
| Load generator | enabled | disabled |
| Replica counts | 1 | 2–3 |

