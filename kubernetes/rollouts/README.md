# Argo Rollouts Guide: Canary, Blue/Green & Instant Rollback

This directory contains production-ready Argo Rollouts manifests integrated with **Prometheus automated analysis** and **NGINX Ingress traffic routing**.

---

## 1. Quick Overview

| Feature | Description |
|---|---|
| **Canary Deployment** | Progressively routes traffic (20% -> 50% -> 100%) through NGINX Ingress while monitoring real user telemetry. |
| **Blue/Green Deployment** | Deploys new version alongside existing version (`previewService`), runs health checks, and cuts over traffic once approved. |
| **Prometheus SLI Tracing** | Automatically queries Prometheus for **HTTP 5xx error rate** (> 2%) and **P95 Latency** (> 500ms). |
| **Automated Rollback** | If Prometheus alerts or SLIs breach thresholds during a canary step, Argo Rollouts immediately aborts and rolls back to stable. |
| **Manual Rollback** | Instant 1-command rollback to the previous version or any historical revision. |

---

## 2. Installing the Argo Rollouts CLI

To monitor and control rollouts from your terminal:

### On Windows (PowerShell):
```powershell
curl.exe -LO "https://github.com/argoproj/argo-rollouts/releases/latest/download/kubectl-argo-rollouts-windows-amd64.exe"
Move-Item kubectl-argo-rollouts-windows-amd64.exe "$HOME\AppData\Local\Microsoft\WindowsApps\kubectl-argo-rollouts.exe"
```

### On macOS / Linux:
```bash
brew install argoproj/tap/kubectl-argo-rollouts
# Or curl:
curl -LO https://github.com/argoproj/argo-rollouts/releases/latest/download/kubectl-argo-rollouts-linux-amd64
chmod +x ./kubectl-argo-rollouts-linux-amd64
sudo mv ./kubectl-argo-rollouts-linux-amd64 /usr/local/bin/kubectl-argo-rollouts
```

---

## 3. How to Trigger and Watch a Canary Rollout

### 1. Apply Analysis Templates & Rollout
```bash
kubectl apply -f kubernetes/rollouts/analysis-templates.yaml
kubectl apply -f kubernetes/rollouts/frontend-canary-rollout.yaml
```

### 2. Watch the Rollout Progression in Real-Time
```bash
kubectl argo rollouts get rollout frontend -n online-boutique --watch
```

### 3. Deploy a New Image
When you deploy a new image tag:
```bash
kubectl argo rollouts set image frontend server=965085140318.dkr.ecr.il-central-1.amazonaws.com/online-boutique/frontend:NEW_TAG -n online-boutique
```

### 4. Progression Flow:
1. **Weight 20%:** 20% of live traffic routes to canary pods via NGINX Ingress annotations. Pauses for 1 minute.
2. **Prometheus Telemetry Trace:** Argo Rollouts queries Prometheus:
   - `frontend-error-rate`: Verifies HTTP 5xx errors remain $\le 2\%$.
   - `frontend-latency`: Verifies P95 response time remains $\le 500\text{ ms}$.
3. **Weight 50%:** If analysis succeeds, traffic steps to 50% for 2 minutes.
4. **Promotion:** Once all analysis runs pass, the new revision becomes 100% stable, and old replica sets are scaled down.

---

## 4. Manual Rollback & Abort Commands

You can instantly intervene at any point:

### 🔴 Instant Rollback to Previous Image (1 Command):
```bash
kubectl argo rollouts undo frontend -n online-boutique
```
*This immediately reverts the active rollout back to the last stable revision.*

### 🛑 Abort an In-Progress Canary Release Immediately:
If you notice unexpected behavior before automated analysis finishes:
```bash
kubectl argo rollouts abort frontend -n online-boutique
```
*This immediately shifts 100% of traffic back to the stable replica set and sets the rollout state to Degraded.*

### 🔁 Rollback to a Specific Historical Revision:
```bash
# 1. View revision history
kubectl argo rollouts history rollout frontend -n online-boutique

# 2. Revert to revision 1 (or any revision number)
kubectl argo rollouts undo frontend --to-revision=1 -n online-boutique
```

### 🚀 Manual Step Promotion (Skip Pause):
```bash
kubectl argo rollouts promote frontend -n online-boutique
```

---

## 5. Web UI Dashboard

Argo Rollouts includes a dedicated real-time dashboard:

```bash
kubectl argo rollouts dashboard -n argo-rollouts
```
Open your browser to: **`http://localhost:3100`**

From this UI, you can:
- Visually inspect active pods, canary weight, and revision history.
- View live metric graphs from Prometheus analysis runs.
- Click **"Promote"**, **"Pause"**, **"Abort"**, or **"Rollback"** with a single click.
