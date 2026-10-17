# Local End-to-End (E2E) Environment Guide

## 1. Overview
The local end-to-end environment (`docker-compose.e2e.yml`) allows testing the entire Online Boutique architecture without deploying Kubernetes or incurring AWS costs.

It runs:
- **11 Boutique Microservices**: `frontend`, `cartservice`, `productcatalogservice`, `currencyservice`, `paymentservice`, `shippingservice`, `emailservice`, `checkoutservice`, `recommendationservice`, `adservice`, and `inventoryservice`.
- **Database & Cache**: PostgreSQL 16 (`boutique_db`), Redis 7.
- **Async Event Bus**: LocalStack 3.8 (emulating AWS SNS & SQS with dead-letter queues and raw message delivery fan-out matching Terraform names).
- **Identity & Access**: Keycloak 24 (`online-boutique` realm with pre-configured dev client secret `boutique-frontend-secret-key` and pre-seeded test customer).
- **DB Init / Migration**: Ephemeral container running exact SQL extracted on-the-fly from Helm's `db-init-job.yaml` (zero schema forking).

---

## 2. How to Run Locally

### Bash / Linux / WSL / macOS
```bash
./scripts/e2e-local.sh
```

### Windows PowerShell
```powershell
.\scripts\e2e-local.ps1
```

### Manual Teardown
```bash
docker compose -f docker-compose.e2e.yml down -v
```

---

## 3. What This Environment Verifies
1. **Full gRPC Service Mesh Wiring**: Product catalog, cart, recommendations, ads, payments, currency conversion, and shipping quotes over gRPC.
2. **Database Migrations & Constraints**: Products catalog seed and inventory ledger schema from `db-init-job.yaml`.
3. **Inventory Lua Holds & Validation**: Reserving items, expiring holds, CAS commits upon order checkout, and decrementing Postgres inventory total stock.
4. **Checkout Idempotency**: Atomic Redis locking, deterministic order ID generation, duplicate order rejection, and retry safety.
5. **Transactional Outbox Pattern**: Inserting `OrderPlaced` events within the database transaction, polling with `FOR UPDATE SKIP LOCKED`, and confirming published events.
6. **SNS Fan-Out to Multiple SQS Queues**: Emulating AWS SNS topic `online-boutique-dev-order-events` fanning out to `online-boutique-dev-order-events-email` and `online-boutique-dev-order-events-shipping`.
7. **Consumer Idempotency & Two-Phase Dedup**: Both `emailservice` and `shippingservice` processing events, generating tracking numbers and emails, and acknowledging messages from SQS.
8. **End-to-End Keycloak OIDC Flow**: Browser authentication code flow, token exchange with client secret, Redis session storage, and guest cart migration.

---

## 4. What This Environment CANNOT Verify (Requires Live AWS Run)

The local Docker Compose environment intentionally abstracts certain AWS cloud infrastructure components. The following remain to be validated during the AWS deployment run:

| Cloud Feature | Why It Cannot Be Verified Locally | Validation Strategy on AWS |
| :--- | :--- | :--- |
| **IRSA (IAM Roles for Service Accounts)** | LocalStack uses dummy test credentials (`AWS_ACCESS_KEY_ID=test`). EKS Webhook token projection (`aws-iam-token`), OIDC provider trust relationships, and IAM AssumeRoleWithWebIdentity policies require real AWS IAM. | Verified via `tests/smoke` checking pod service accounts and AWS STS caller identity. |
| **ElastiCache TLS & AUTH** | Local Redis runs in standalone mode. In Terraform, ElastiCache is **cluster-mode disabled** (single primary node with read replica in Multi-AZ, transit encryption TLS 1.2+, and AUTH token). Tested locally via `docker-compose.e2e.redis-secure.yml`. | Verified via smoke tests verifying Redis connection over TLS with token auth from pod. |
| **AWS Application Load Balancer (ALB)** | Local frontend binds directly to port 8080. Ingress-NGINX / AWS Load Balancer Controller provisioning an AWS Application Load Balancer, ACM TLS certificates, and listener rules are AWS-specific. | Validated in CI workflow (`up.yml`) and smoke tests hitting the live external ALB URL. |
| **ExternalSecrets Operator (ESO)** | Local Compose injects environment variables directly. ExternalSecrets controller synchronizing AWS Secrets Manager secrets into Kubernetes Secret objects requires Kubernetes CRDs and AWS IAM. | Verified via `kubectl get externalsecret,secret -n online-boutique` in cluster deployment. |
| **Karpenter Node Autoscaling** | Local Docker Compose does not simulate EC2 node scaling, NodePools, EC2NodeClasses, or AWS interruption queues. | Verified during load tests (e.g., K6) observing node creation and scale-down in AWS EKS. |

---

## 5. Keycloak URL / Path Parity and ALB Architecture

### Path Parity
- **Relative Path**: Both the Helm chart (`httpRelativePath=/auth`) and Docker Compose (`KC_HTTP_RELATIVE_PATH=/auth`) serve Keycloak under `/auth`.
- **Environment Variables**:
  - `KEYCLOAK_URL`: Public-facing browser endpoint (`http://localhost:8082/auth` locally; `https://<ALB-DNS-or-Custom-Domain>/auth` on AWS).
  - `KEYCLOAK_INTERNAL_URL`: In-cluster / internal service endpoint (`http://keycloak:8080/auth` locally; `http://keycloak.keycloak.svc.cluster.local:8080/auth` on AWS).
  - `KEYCLOAK_REALM`: `online-boutique`

### Keycloak Behind AWS Application Load Balancer (ALB)
When deployed behind an AWS ALB where the external hostname is dynamic or not known in advance (e.g. `k8s-onlinebo-...elb.amazonaws.com` before CNAME assignment):
1. **`KC_PROXY_HEADERS=xforwarded`**:
   Configures Quarkus/Keycloak to trust reverse proxy headers sent by the ALB (`X-Forwarded-For`, `X-Forwarded-Proto=https`, `X-Forwarded-Host`, `X-Forwarded-Port`).
2. **`KC_HOSTNAME_STRICT=false`**:
   Disables strict hostname matching so Keycloak dynamically resolves the authority from incoming request headers instead of rejecting requests on unknown hostnames.
3. **`KC_HTTP_RELATIVE_PATH=/auth`**:
   Ensures Keycloak handles path-based routing rules configured on the ALB listener or Ingress resource.

### Frontend Redirect URI and Issuer Derivation
- When a user initiates login, `frontend` derives the redirect URI dynamically:
  - If `KEYCLOAK_REDIRECT_URL` is set, that URL is used.
  - Otherwise, it inspects `r.Header.Get("X-Forwarded-Proto")` (defaulting to `http` or `https`) and `r.Host` (or `X-Forwarded-Host`) to construct `{proto}://{host}/callback`.
- The frontend initiates the OIDC authorization code flow pointing the browser to `KEYCLOAK_URL/realms/online-boutique/protocol/openid-connect/auth`.
- Upon callback, the frontend exchanges the authorization code with Keycloak using `KEYCLOAK_INTERNAL_URL` (direct backend HTTP request) or `KEYCLOAK_URL`.
- The Keycloak realm configuration (`online-boutique-realm.json`) permits wildcard / dynamic redirect URIs for the `boutique-frontend` client.

---

## 6. Redis TLS + AUTH Verification (`docker-compose.e2e.redis-secure.yml`)

The compose stack supports full TLS 1.2+ encryption and password authentication via the override file:
```bash
docker compose -f docker-compose.e2e.yml -f docker-compose.e2e.redis-secure.yml up -d
```
- **Redis Server**: Runs with `--requirepass boutique_redis_pass --port 0 --tls-port 6379` with TLS certificates generated in `certs/`.
- **Go Services (`frontend`, `inventoryservice`, `checkoutservice`, `productcatalogservice`, `shippingservice`)**:
  Support `REDIS_TLS=true` (using `tls.Config{InsecureSkipVerify: true}`) and `REDIS_AUTH` / `REDIS_PASSWORD`.
- **Python Service (`emailservice`)**:
  Supports `ssl=True` and `ssl_cert_reqs=None`.
- **C# .NET Service (`cartservice`)**:
  Uses `StackExchange.Redis`. In .NET, `SslStream` verifies certificate revocation by default. In local testing with self-signed certificates, `cartservice` requires:
  1. Mounting the root CA: `./certs/ca.crt:/etc/ssl/certs/ca-certificates.crt:ro`.
  2. Connection string: `redis:6379,password=boutique_redis_pass,ssl=true,sslProtocols=Tls12,checkCertificateRevocation=false,abortConnect=false`.
  *(Note: In AWS ElastiCache, Amazon Trust Services provides publicly trusted certificates, so custom CA mounting and revocation disabling are not required).*

---

## 7. Database State & Outbox Publishing Analysis

### Historical Discrepancy (43 Orders vs 30 Outbox Events)
During early testing, the database showed 43 orders but only 30 published outbox events due to:
1. **Direct Integration Tests**: Tests such as `TestIdempotencyDuplicateOrdersDifferentKeys` and `TestIdempotencyRetriedOrderSameResult` inserted order rows directly via SQL to test concurrency locks and table constraints without invoking checkoutservice's asynchronous outbox publisher worker.
2. **Container Restarts**: The outbox publisher in checkoutservice runs on a 500ms ticker. During development iterations, containers were stopped or recreated before unpublished rows were processed.
3. **Pending / Failed Orders**: Any orders left in `PENDING` state never trigger an outbox event.

### Clean Run Verification
Starting from a clean state (`down -v`):
- **Initial State**: 0 orders, 0 unpublished outbox rows, 10 units in stock.
- **After E2E Run**: Exactly 1 `CONFIRMED` order, exactly 1 published outbox event (`published_at` timestamp set), 0 unpublished events (`published_at IS NULL`), and 9 units in stock. Both email and shipping SQS consumers processed the order and cleared queues (0 in queue, 0 in DLQ).
