# PoC 1.2: Intent-Driven Application Scaling

A Kubernetes-native intent management system where an operator expresses
a business goal (latency SLA) as a custom resource, and a controller
automatically scales the target deployment to meet it.

## Architecture

![Architecture](diagrams/architecture.svg)

## Scaling Flow

![Flow](diagrams/flow.svg)

## Example

```yaml
apiVersion: an.openshift.io/v1alpha1
kind: ApplicationIntent
metadata:
  name: keep-latency-low
spec:
  target:
    deployment: sample-app
  sla:
    p99LatencyMs: 50
  constraints:
    minReplicas: 1
    maxReplicas: 10
```

```
$ oc get applicationintent
NAME               TARGET       STATE       P99MS   REPLICAS   SLA
keep-latency-low   sample-app   Fulfilled   49      1          50

# Under load:
NAME               TARGET       STATE     P99MS   REPLICAS   SLA
keep-latency-low   sample-app   Scaling   274     4          50
```

## Components

| Component | Description |
|-----------|-------------|
| Intent Controller | Go/kubebuilder operator, queries Prometheus, scales deployments |
| ApplicationIntent CRD | Custom resource expressing latency SLA + scaling constraints |
| Sample App | Go HTTP server with latency that increases under load |
| ServiceMonitor | Prometheus scraping config for the sample app |

## Prerequisites

- OpenShift 4.19+ with user workload monitoring enabled
- Prometheus (via OpenShift Monitoring stack)

## Deployment

```bash
# 1. Enable user workload monitoring
oc apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: cluster-monitoring-config
  namespace: openshift-monitoring
data:
  config.yaml: |
    enableUserWorkload: true
EOF

# 2. Create namespace
oc new-project poc-1-2

# 3. Build and deploy sample app
oc new-build --binary --name=sample-app --strategy=docker -n poc-1-2
oc start-build sample-app --from-dir=sample-app/ -n poc-1-2 --follow
oc apply -f sample-app/deployment.yaml

# 4. Install CRD
cd controller && make install

# 5. Build and deploy controller
oc new-build --binary --name=intent-controller --strategy=docker -n poc-1-2
oc start-build intent-controller --from-dir=. -n poc-1-2 --follow
# Deploy controller (ServiceAccount, RBAC, Deployment)

# 6. Create intent
oc apply -f test/intent-example.yaml

# 7. Generate load
oc apply -f test/load-generator.yaml
watch oc get applicationintent -n poc-1-2
```

## Scaling Logic

- **p99 > SLA**: Scale up by 1 replica (up to maxReplicas) → state: `Scaling`
- **p99 > SLA at maxReplicas**: No scaling possible → state: `Degraded`
- **p99 < SLA/2**: Scale down by 1 replica (down to minReplicas) → state: `Scaling`
- **SLA/2 ≤ p99 ≤ SLA**: No change → state: `Fulfilled`

## Integration with PoC 4.3 (NOC Assistant)

The NOC Assistant can manage ApplicationIntent CRs via natural language:

- "What intents are active?" → queries CRs via MCP
- "What SLA should I set for sample-app?" → analyzes historical Prometheus
  metrics and recommends a target
- "Create an intent for sample-app with p99 under 75ms" → creates the CR
  via MCP write access

See [poc-4.3/](../poc-4.3/) for details.

## What It Proves

The Kubernetes reconciliation loop can express and fulfill network-style
intents. This is the foundational pattern for TMF921-aligned intent
management in telco autonomous networks.
