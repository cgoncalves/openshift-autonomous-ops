# PoC 1.1: Agentic Cluster Self-Healing

An AI-driven self-healing system for OpenShift that autonomously detects,
diagnoses, and remediates cluster issues — without human intervention.

## Overview

An AI agent running on OpenShift AI detects a degraded node (high memory
pressure, disk I/O errors), reasons about the best remediation, and uses
Ansible Automation Platform to cordon, drain, and repair the node.

The system implements a closed loop: **observe** (via MCP), **decide**
(via LLM), **act** (via AAP), **verify** (via MCP). Every action is
auditable through the decision ledger and AAP job history.

## Architecture

![Architecture](diagrams/architecture.svg)

## Use Case Flow

![Flow](diagrams/flow.svg)

## Demo Scenario

1. **Inject fault**: Deploy a stress pod on a worker node that triggers
   `MemoryPressure` or `DiskPressure` conditions
2. **Detection**: Prometheus detects the condition, AlertManager fires
   an alert, and sends webhooks to both the agent and EDA
3. **Triage** (~20s): The Triage Agent queries real cluster state via
   MCP tools (node metrics, events, alerts) and produces a structured
   diagnosis with root cause and confidence level
4. **Remediation** (~5s): The Remediation Agent decides the appropriate
   action (`cordon_and_drain`, `restart_pods`, `escalate_to_human`, or
   `no_action_needed`) and triggers an AAP job template
5. **Execution**: AAP runs the remediation playbook — cordon the node,
   drain pods (respecting PDBs), wait for stabilization, uncordon
6. **Verification** (~19s): The Verification Agent confirms the node is
   healthy, pods are rescheduled, and alerts have cleared
7. **Decision Ledger**: Full reasoning chain returned as structured JSON
   with timestamps for each phase

**Total loop time: ~44 seconds** (with external Qwen 3.6 35B model)

## What It Proves

- OpenShift can host AI agents that **autonomously manage the platform
  itself** — the agent runs on the same cluster it manages
- The **closed loop** (observe via MCP → decide via LLM → execute via
  Ansible → verify via MCP) works end-to-end
- **Multi-agent coordination** works: triage, remediation, and
  verification are separate reasoning steps with distinct system prompts
- Every action is **auditable** through the decision ledger, AAP job
  history, and EDA event logs
- **Alert deduplication** prevents runaway remediation — the same
  event+node pair is only processed once per cooldown period

## Components

| Component | Namespace | Description |
|-----------|-----------|-------------|
| OpenShift AI (RHOAI 3.4) | `redhat-ods-operator` | AI platform operator |
| LlamaStack | `llama-stack` | LLM inference proxy (Responses API with MCP tools) |
| Self-Healing Agent | `llama-stack` | Multi-agent FastAPI service (triage/remediation/verification) |
| OpenShift MCP Server | `mcp-system` | Cluster observation via Streamable HTTP ([shared](../shared/mcp-server/)) |
| AAP 2.7 Controller | `aap` | Executes remediation playbooks via job templates |
| AAP 2.7 EDA | `aap` | Event-driven detection, forwards alerts via `run_job_template` |
| PostgreSQL | `llama-stack` | LlamaStack metadata store |

## Prerequisites

- OpenShift 4.19+
- [Shared infrastructure](../shared/) deployed (MCP Server, LlamaStack)
- AAP subscription manifest (apply via AAP UI after deployment)
- External LLM API (OpenAI-compatible) or local vLLM on GPU

## Deployment

```bash
# 1. Deploy shared infrastructure
oc apply -f ../shared/mcp-server/deployment.yaml
oc apply -f ../shared/mcp-server/rbac.yaml

# 2. Deploy AAP Operator and platform (includes EDA gateway URL fix)
oc apply -f aap/operator.yaml
# Wait for operator...
oc apply -f aap/platform.yaml
# Apply AAP license via UI

# 3. Deploy AAP remediation RBAC
oc apply -f aap/remediation-rbac.yaml

# 4. Deploy RHOAI + LlamaStack (see RHOAI 3.4 docs)

# 5. Build and deploy the agent
oc new-build --binary --name=self-healing-agent --strategy=docker -n llama-stack
oc start-build self-healing-agent --from-dir=agent/ -n llama-stack --follow
oc apply -f agent/deployment.yaml

# 6. Create EDA rulebook activation via API or UI

# Or use the Makefile:
make deploy
```

## Configuration

### Environment Variables (Agent)

| Variable | Default | Description |
|----------|---------|-------------|
| `LLAMASTACK_URL` | `http://lsd-granite-milvus-inline-service.llama-stack.svc:8321` | LlamaStack API |
| `MCP_SERVER_URL` | `http://openshift-mcp-server.mcp-system.svc:8001/mcp` | MCP server endpoint |
| `AAP_URL` | `https://aap-aap.apps.<cluster>` | AAP Controller API |
| `AAP_TOKEN` | (from secret) | AAP OAuth token |
| `MODEL_ID` | `vllm-inference/Qwen3.6-35B-A3B` | LLM model ID |
| `COOLDOWN_SECONDS` | `600` | Dedup cooldown per event+node |

## Testing

```bash
# Manual agent test
oc exec -n llama-stack deploy/self-healing-agent -- \
  curl -s -X POST http://localhost:8000/api/remediate \
  -H 'Content-Type: application/json' \
  -d '{"event_type":"MemoryPressure","node_name":"<node>","severity":"critical"}'

# Fault injection
oc apply -f test/fault-injection.yaml

# Check status
make status
```

## Known Issues

### AAP 2.7: EDA `run_job_template` authentication

The EDA operator defaults `EDA_CONTROLLER_URL` to `http://aap-controller-service` (direct to
controller), but the controller only accepts JWT-authenticated requests routed through the
gateway. The `ansible-rulebook` code already supports gateway routing
([PR #654](https://github.com/ansible/ansible-rulebook/issues/652)) — it auto-detects gateway
URLs and uses the correct API paths.

**Fix**: Set `automation_server_url` in the `AnsibleAutomationPlatform` CR (already included
in [`aap/platform.yaml`](aap/platform.yaml)):

```yaml
spec:
  eda:
    automation_server_url: "http://aap/api/controller"
```

This propagates through: parent AAP CR → EDA CR → configmap → activation pods.

## Playbooks

Ansible playbooks for node remediation are maintained in a separate repository:
https://github.com/cgoncalves/openshift-self-healing-playbooks
