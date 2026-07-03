# PoC 4.3: MCP-Integrated NOC Assistant

A natural-language chat interface where NOC operators query cluster health,
investigate issues, manage intents, and get AI-driven recommendations — all
through conversation.

## Overview

NOC operators typically interact with their cluster through `kubectl`,
dashboards, and runbooks. This PoC replaces that workflow with a
conversational interface: the operator asks questions in plain English,
and an AI assistant queries real cluster data via the Model Context
Protocol (MCP), reasons about it, and responds with structured answers.

The assistant acts as a **human-in-the-loop stepping stone to full
autonomy** — operators can query, diagnose, and (in future) execute
changes through natural language, with every action governed by RBAC.

## Architecture

![Architecture](diagrams/architecture.svg)

## Use Case Flow

![Flow](diagrams/flow.svg)

## Demo Scenario

**Scenario 1 — Cluster health check:**
```
Operator: "What's the health of the cluster?"
Assistant: [calls nodes_top, alertmanager_alerts via MCP]
           "5 nodes, all healthy.
            • Worker 1: CPU 9%, Memory 49%
            • Worker 2: CPU 3%, Memory 58%
            • 1 alert active: AlertmanagerFailedToSendAlerts (warning)"
```

**Scenario 2 — Investigate a problem:**
```
Operator: "Why is pod sample-app-xyz crash-looping?"
Assistant: [calls events_list, pods_log via MCP]
           "Pod restarted 5 times in 10 min. Last log: OOMKilled.
            Current memory limit: 256Mi. Peak usage: 312Mi.
            Recommendation: increase memory limit to 512Mi."
```

**Scenario 3 — SLA recommendation:**
```
Operator: "What SLA should I set for sample-app?"
Assistant: [calls prometheus_query_range for 24h p99 data]
           "Based on 24h of data, your p99 peaks at 48ms during
            business hours. I recommend an SLA of 75ms with max
            8 replicas. Here's the YAML to apply..."
```

**Scenario 4 — Intent management:**
```
Operator: "What intents are active in poc-1-2?"
Assistant: [calls resources_list for ApplicationIntent CRs]
           "1 intent: keep-latency-low
            Target: sample-app | SLA: 50ms | State: Fulfilled
            Current p99: 49ms | Replicas: 1"
```

## What It Proves

- **MCP transforms OpenShift into a conversational platform** — operators
  interact with the cluster through natural language instead of `kubectl`
- The assistant queries **real cluster data** (not simulated) via MCP
  tools — node metrics, pod status, events, alerts, Prometheus queries
- The LLM **reasons about the data** — it doesn't just dump raw output,
  it correlates, diagnoses, and recommends
- The same MCP tools work for **any question** — the operator doesn't
  need to know which tool to use, the LLM figures it out from context
- This is the **human-in-the-loop** pattern that builds trust before
  full autonomy (PoC 1.1)

## Components

Reuses the existing infrastructure from PoC 1.1 and integrates with
PoC 1.2 (Intent-Driven Scaling):

| Component | Namespace | Description |
|-----------|-----------|-------------|
| NOC Assistant (Gradio) | `llama-stack` | Chat UI for operators |
| LlamaStack | `llama-stack` | LLM inference proxy (Responses API with MCP tool support) |
| OpenShift MCP Server | `mcp-system` | Cluster observation via Streamable HTTP ([shared](../shared/mcp-server/)) |
| External LLM | external | Qwen 3.6 35B via LiteLLM (OpenAI-compatible) |
| Intent Controller | `poc-1-2` | Fulfills ApplicationIntent CRs (PoC 1.2, optional) |

## Prerequisites

- [Shared infrastructure](../shared/) deployed (MCP Server, LlamaStack)
- PoC 1.2 deployed (ApplicationIntent CRD + controller) for intent
  management (optional — assistant works without it)

## Deployment

```bash
# Build the container image
oc new-build --binary --name=noc-assistant --strategy=docker -n llama-stack
oc start-build noc-assistant --from-dir=app/ -n llama-stack --follow

# Deploy
oc apply -f app/deployment.yaml

# Or use the Makefile:
make deploy
```

## Usage

Open the Route URL in a browser:
```
https://noc-assistant-llama-stack.apps.<cluster-domain>
```

### Example Queries

| Query | What the assistant does |
|-------|------------------------|
| "What's the health of the cluster?" | Queries nodes_top, alertmanager_alerts → summarizes |
| "Show me pods in the aap namespace" | Calls pods_list_in_namespace → displays results |
| "Why is pod X crash-looping?" | Queries events_list, pods_log → diagnoses root cause |
| "What alerts are currently firing?" | Queries alertmanager_alerts → lists active alerts |
| "What's the CPU usage on each node?" | Calls nodes_top → formats usage table |
| "What intents are active?" | Queries ApplicationIntent CRs → shows state, p99, replicas |
| "What SLA should I set for sample-app?" | Queries prometheus_query_range for historical p99 → recommends |
| "Create an intent with p99 under 75ms" | Generates correct YAML for the operator to apply* |

\* The MCP server v0.2 only exposes read tools. Write tools (`resources_create_or_update`)
exist in the [source code](https://github.com/openshift/openshift-mcp-server) but haven't
shipped yet. When a newer image is available, intent creation will work end-to-end without
any changes to the NOC Assistant.

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LLAMASTACK_URL` | `http://lsd-granite-milvus-inline-service.llama-stack.svc:8321` | LlamaStack API |
| `MCP_SERVER_URL` | `http://openshift-mcp-server.mcp-system.svc:8001/mcp` | MCP server endpoint |
| `MODEL_ID` | `vllm-inference/Qwen3.6-35B-A3B` | LLM model ID |
