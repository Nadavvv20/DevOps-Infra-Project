# Database Access & Migration Verification Guide

This guide explains how to connect to the AWS RDS / Aurora PostgreSQL database provisioned in your private VPC subnets, verify table migrations, and run interactive SQL queries.

---

## Why Can't I Connect Directly from my Laptop (DBeaver / pgAdmin)?

By security design (Zero-Trust):
1. Aurora/RDS is hosted inside **private subnets** with no public IP address.
2. The Security Group (`sg-aurora`) allows inbound port `5432` traffic **strictly** from the EKS Node Security Group (`sg-node`).

This prevents external internet access and keeps database credentials and traffic secure.

---

## Method 1: Interactive Debug Pod (Fastest & Recommended)

Launch an ephemeral, interactive PostgreSQL client pod in Kubernetes that pulls credentials directly from the `aurora-credentials` Secret:

```bash
kubectl run -it --rm psql-client \
  --namespace online-boutique \
  --image=postgres:16-alpine \
  --restart=Never \
  --env-from=secret/aurora-credentials \
  -- sh -c 'PGPASSWORD=$DB_PASSWORD psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME'
```

### Essential `psql` Commands:
Once inside the prompt (`online_boutique=>`):
- `\l` — List all databases.
- `\dt` — List all tables in the current schema.
- `\d+ <table_name>` — Describe table structure, columns, indexes, and foreign keys.
- `SELECT * FROM orders LIMIT 5;` — Query rows.
- `\q` — Exit the prompt (the pod will automatically be deleted).

---

## Method 2: Local Port-Forwarding (For DBeaver / DataGrip / pgAdmin)

If you prefer using a desktop GUI tool:

### 1. Launch a socat proxy pod in Kubernetes:
```bash
# 1. Fetch DB Host from secret
DB_HOST=$(kubectl get secret aurora-credentials -n online-boutique -o jsonpath='{.data.DB_HOST}' | base64 --decode)

# 2. Run proxy pod
kubectl run db-proxy \
  --namespace online-boutique \
  --image=alpine/socat \
  --restart=Never \
  -- "tcp-listen:5432,fork,reuseaddr" "tcp:${DB_HOST}:5432"

# 3. Port-forward to your local machine
kubectl port-forward pod/db-proxy 5432:5432 -n online-boutique
```

### 2. Connect your GUI tool:
- **Host:** `localhost` (or `127.0.0.1`)
- **Port:** `5432`
- **Database:** `online_boutique`
- **Username & Password:** Extract from Secret:
  ```powershell
  # Windows PowerShell:
  [System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String((kubectl get secret aurora-credentials -n online-boutique -o jsonpath='{.data.DB_PASSWORD}')))
  ```

---

## Method 3: Automated Migration & Schema Verification Job

To automatically create and verify all tables with one command:

```bash
kubectl apply -f kubernetes/database/init-schema-job.yaml
```

View the detailed verification output:
```bash
kubectl logs -n online-boutique job/db-schema-migration-verifier -f
```

The job outputs:
- Connection status
- Complete list of tables
- Column types and indexes
- Table row counts
