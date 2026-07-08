# PoC 1.4: AI-Assisted Intent Configuration

AI translates business intents into Kubernetes configurations at design
time. Standard controllers handle runtime. No LLM in the control loop.

## Overview

Instead of an LLM making real-time scaling decisions (too slow, expensive,
and risky for production), the AI acts as a **capacity planner**:

1. **Design time**: Operator creates an intent. The AI analyzes the workload
   and generates HPA, PDB, and resource configurations for human review.
2. **Runtime**: Standard Kubernetes controllers (HPA) handle scaling —
   fast, deterministic, battle-tested.
3. **Exception**: When runtime can't cope, the controller re-invokes the
   LLM with runtime context to generate an updated recommendation.

## Architecture

![Architecture](diagrams/architecture.svg)

## Flow

![Flow](diagrams/flow.svg)

## Demo Scenario

1. **Create intent**:
   ```bash
   oc apply -f test/intent-example.yaml
   ```

2. **AI analyzes** (~30s): Controller calls LLM, generates recommendation
   ```
   $ oc get applicationintent
   NAME             TARGET       PHASE             APPROVED   REPLICAS
   sample-app-sla   sample-app   PendingApproval
   ```

3. **Review recommendation**:
   ```bash
   oc get applicationintent sample-app-sla -o jsonpath='{.status.recommendation.summary}'
   oc get applicationintent sample-app-sla -o jsonpath='{range .status.recommendation.resources[*]}{.kind}/{.name}{"\n"}{end}'
   ```

4. **Approve**:
   ```bash
   oc patch applicationintent sample-app-sla --type=merge --subresource=status \
     -p '{"status":{"approved":true}}'
   ```

5. **Resources created**: HPA + PDB applied automatically
   ```
   $ oc get hpa,pdb
   NAME                        REFERENCE               TARGETS          MINPODS   MAXPODS
   sample-app-hpa              Deployment/sample-app   cpu: 12%/70%     2         10

   NAME                        MIN AVAILABLE   ALLOWED DISRUPTIONS
   sample-app-pdb              1               1
   ```

6. **Runtime scaling**: HPA handles load changes — no LLM involved

7. **Escalation** (if SLA breached beyond HPA capacity):
   - HPA at max replicas, CPU still above target for >90s
   - Controller re-invokes LLM with runtime context
   - LLM generates updated recommendation (e.g., increased CPU limits,
     pod anti-affinity, HPA behavior tuning)
   - New `PendingApproval` for operator review

## Demo Results

![Demo Results](diagrams/demo-results.svg)

## What It Proves

- AI at **design time** is safer, cheaper, and more predictable than AI
  in a runtime control loop
- The LLM generates **production-quality configs** with reasoning
  (e.g., "Increased CPU from 200m to 400m to allow bursting for p99
  optimization")
- **Human-in-the-loop** approval builds trust — start with manual
  review, graduate to `autoApprove: true` as confidence grows
- Standard K8s controllers (HPA, PDB) are the right tool for runtime —
  the AI's value is in **configuring them correctly**

## Components

| Component | Description |
|-----------|-------------|
| Intent Controller | Go/kubebuilder, calls LLM at design time, monitors HPA at runtime |
| ApplicationIntent CRD | Objectives (latency, availability) + constraints + recommendation + fulfillment |
| Sample App | Go HTTP server (reused from PoC 1.2) |

## Prerequisites

- OpenShift 4.19+
- External LLM API (OpenAI-compatible, e.g., LiteLLM)

## CRD Example

```yaml
apiVersion: an.openshift.io/v1alpha1
kind: ApplicationIntent
metadata:
  name: sample-app-sla
spec:
  target:
    deployment: sample-app
    namespace: poc-1-4
  objectives:
    - type: Latency
      metric: p99
      target: "50ms"
    - type: Availability
      target: "99.9%"
  constraints:
    minReplicas: 2
    maxReplicas: 10
    maxCPUPerPod: "500m"
    maxMemoryPerPod: "256Mi"
  autoApprove: false
```

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LLAMASTACK_URL` | LiteLLM endpoint | LLM API base URL (OpenAI-compatible) |
| `MODEL_ID` | `Qwen3.6-35B-A3B` | LLM model ID |
| `LLM_API_KEY` | (from env) | API key for the LLM endpoint |
