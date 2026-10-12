# DevOps Infra Project — Online Boutique on AWS EKS

Production-grade reference architecture for deploying **Google's Online Boutique** (microservices-demo) on **AWS EKS** using Terraform, Karpenter, ArgoCD, and GitHub Actions.

---

## Architecture Overview

```
                             ┌──────────────────────────────────────────────────────────────┐
                             │                     AWS Account                               │
                             │                                                              │
  Developer ──── PR ────▶  GitHub Actions  ─────────────────────────────────────────────── │
                             │     │                                                        │
                             │     ├── terraform-plan.yml  ──▶  Terraform Plan + Checkov   │
                             │     ├── build-scan.yml      ──▶  ECR + Trivy scan           │
                             │     └── deploy.yml          ──▶  ArgoCD sync                │
                             │                                                              │
  User ──── HTTPS ──────▶  Route53 ──▶ ACM ──▶ AWS ALB ──▶ EKS Ingress                  │
                             │                               │                              │
                             │   ┌───────────────────────────┼────────────────────────┐    │
                             │   │          EKS Cluster       │                        │    │
                             │   │                            ▼                        │    │
                             │   │   ┌────────────────────────────────────────────┐   │    │
                             │   │   │   online-boutique namespace                │   │    │
                             │   │   │  frontend ──▶ productcatalog               │   │    │
                             │   │   │     │         currencyservice              │   │    │
                             │   │   │     ▼         paymentservice               │   │    │
                             │   │   │  checkout ──▶ emailservice                 │   │    │
                             │   │   │     │         shippingservice              │   │    │
                             │   │   │     ▼                                      │   │    │
                             │   │   │  cartservice ──▶ ElastiCache Redis         │   │    │
                             │   │   └────────────────────────────────────────────┘   │    │
                             │   │                                                     │    │
                             │   │   kube-system: Karpenter, ALB Controller, CoreDNS  │    │
                             │   │   argocd:      ArgoCD server + applicationset      │    │
                             │   │   monitoring:  Prometheus + Grafana + AlertManager │    │
                             │   │   external-secrets: ESO operator                   │    │
                             │   └─────────────────────────────────────────────────────┘    │
                             │                                                              │
                             │   Aurora PostgreSQL (Multi-AZ)    ElastiCache Redis (Multi-AZ)│
                             │   AWS Secrets Manager              CloudWatch Logs            │
                             └──────────────────────────────────────────────────────────────┘
```

---

## Repository Structure

```
.
├── .github/
│   └── workflows/
│       ├── terraform-plan.yml    # IaC lint, validate, plan on PRs
│       ├── build-scan.yml        # Build images, Trivy CVE scan, push ECR
│       └── deploy.yml            # ArgoCD sync + smoke tests
│
├── terraform/
│   ├── modules/
│   │   ├── vpc/                  # VPC, subnets, NAT gateways
│   │   ├── eks/                  # EKS cluster + addons + EBS CSI
│   │   ├── karpenter/            # Karpenter IAM, SQS interruption queue
│   │   ├── rds-aurora/           # Aurora PostgreSQL + Secrets Manager
│   │   ├── elasticache/          # ElastiCache Redis + Secrets Manager
│   │   ├── ecr/                  # ECR repos per service + lifecycle policies
│   │   ├── iam-roles/            # IRSA roles: ESO, ALB Controller, CW Agent
│   │   └── helm-releases/        # Helm installs: Karpenter, ArgoCD, ESO, Prometheus
│   └── environments/
│       ├── dev/                  # Dev-optimized (single NAT, t4g instances)
│       └── prod/                 # HA prod (3 NAT GWs, r7g instances, Multi-AZ)
│
├── kubernetes/
│   ├── argocd/                   # ArgoCD AppProject + Application manifests
│   ├── external-secrets/         # ClusterSecretStore + ExternalSecret CRDs
│   ├── ingress/                  # AWS ALB Ingress for frontend
│   └── monitoring/               # PrometheusRule SLO alerts + ServiceMonitor
│
├── helm/
│   └── online-boutique/          # Wrapper chart over upstream OCI chart
│       ├── Chart.yaml
│       ├── values.yaml           # Base values (ECR, security context, PDBs)
│       ├── values-dev.yaml       # Dev overrides
│       └── values-prod.yaml      # Prod overrides (HPA, higher replicas)
│
└── docs/
    ├── ADR-001-infrastructure-provisioning.md
    ├── ADR-002-data-tier.md
    ├── ADR-003-kubernetes-workload-design.md
    ├── ADR-004-security-secrets.md
    └── ADR-005-observability-slos.md
```

---

## Prerequisites

| Tool | Version | Purpose |
|---|---|---|
| Terraform | >= 1.6 | IaC provisioning |
| AWS CLI | >= 2.15 | Authentication, kubeconfig update |
| kubectl | >= 1.29 | Cluster management |
| helm | >= 3.14 | Chart deployment |
| argocd CLI | latest | GitOps sync management |

---

## Quickstart — Dev Environment

### 1. Bootstrap Terraform State

```bash
# Create S3 bucket and DynamoDB table for state (one-time)
aws s3api create-bucket \
  --bucket my-terraform-state-online-boutique \
  --region us-east-1

aws s3api put-bucket-versioning \
  --bucket my-terraform-state-online-boutique \
  --versioning-configuration Status=Enabled

aws dynamodb create-table \
  --table-name terraform-state-lock \
  --attribute-definitions AttributeName=LockID,AttributeType=S \
  --key-schema AttributeName=LockID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST \
  --region us-east-1
```

Update the `bucket` name in `terraform/environments/dev/main.tf` and `prod/main.tf`.

### 2. Provision Infrastructure

```bash
cd terraform/environments/dev
terraform init
terraform plan -var-file=terraform.tfvars
terraform apply -var-file=terraform.tfvars
```

### 3. Configure kubectl

```bash
aws eks update-kubeconfig \
  --name online-boutique-dev \
  --region us-east-1
```

### 4. Apply Kubernetes Manifests

```bash
# External Secrets
kubectl apply -f kubernetes/external-secrets/

# ArgoCD Application (after ArgoCD is installed by Terraform)
kubectl apply -f kubernetes/argocd/

# Monitoring rules
kubectl apply -f kubernetes/monitoring/
```

### 5. Access the Application

```bash
# Get ArgoCD initial password
kubectl get secret argocd-initial-admin-secret \
  -n argocd -o jsonpath="{.data.password}" | base64 -d

# Get frontend URL
kubectl get ingress -n online-boutique
```

---

## CI/CD Pipeline Flow

```
PR opened
    │
    ├── terraform-plan.yml
    │       tflint → fmt check → terraform validate → terraform plan
    │       Checkov security scan → PR comment with plan output
    │
Push to main
    │
    ├── build-scan.yml
    │       Detect changed services (paths-filter)
    │       Build Docker images (Buildx, layer cache)
    │       Push to ECR (immutable tag = git SHA)
    │       Trivy CVE scan → SARIF upload to GitHub Security
    │       Update image tags in GitOps repo
    │
    └── deploy.yml
            ArgoCD sync → wait for Healthy state
            kubectl rollout status
            Smoke test (curl ALB healthz endpoint)
            Slack alert on failure
```

---

## Key Architectural Decisions

| Decision | Choice | Alternative | Reason |
|---|---|---|---|
| Autoscaler | Karpenter | Cluster Autoscaler | 10× faster provisioning, Spot-aware, consolidation |
| Database | Aurora PostgreSQL | RDS PostgreSQL | 30s failover, live readers, storage auto-scale |
| Cache | ElastiCache Redis | In-cluster StatefulSet | Managed Multi-AZ, zero ops overhead in prod |
| Secrets | ESO + Secrets Manager | Sealed Secrets / Vault | Native IRSA, CloudTrail audit, rotation-aware |
| IaC layout | Dir-per-environment | TF Workspaces | Isolated blast radius, per-env AWS accounts |
| GitOps | ArgoCD | FluxCD | Richer UI, ApplicationSets, RBAC, webhook support |

---

## Interview Reference Docs

| Document | Topics Covered |
|---|---|
| [ADR-001](docs/ADR-001-infrastructure-provisioning.md) | Terraform layout, workspace vs dir-per-env, Karpenter vs CA, prefix delegation |
| [ADR-002](docs/ADR-002-data-tier.md) | Aurora vs RDS cost analysis, Redis StatefulSet vs ElastiCache, workload profiles |
| [ADR-003](docs/ADR-003-kubernetes-workload-design.md) | Deployment vs StatefulSet vs DaemonSet for every service |
| [ADR-004](docs/ADR-004-security-secrets.md) | IRSA deep-dive, ESO secret flow, envelope encryption, threat model |
| [ADR-005](docs/ADR-005-observability-slos.md) | SLO definitions, error budgets, PromQL examples, Prometheus vs CloudWatch |
