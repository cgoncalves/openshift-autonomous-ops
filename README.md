# Autonomous Operations on OpenShift — Proof of Concepts

Proof-of-concept implementations demonstrating AI-driven autonomous
operations on Red Hat OpenShift, progressing from foundational cluster
operations to telco-specific use cases.

## Proof of Concepts

| PoC | Directory | Description | Complexity |
|-----|-----------|-------------|------------|
| 1.1 | [`poc-1.1/`](poc-1.1/) | Agentic Cluster Self-Healing | 2/5 |
| 1.2 | [`poc-1.2/`](poc-1.2/) | Intent-Driven Application Scaling | 2/5 |
| 1.4 | [`poc-1.4/`](poc-1.4/) | AI-Assisted Intent Configuration | 3/5 |
| 4.3 | [`poc-4.3/`](poc-4.3/) | MCP-Integrated NOC Assistant | 3/5 |

## How They Connect

```
  shared/                ← MCP Server, LlamaStack (deploy first)
      ↑
      ├── poc-1.1        ← Self-Healing Agent (MCP + LLM + AAP)
      ├── poc-1.2        ← Intent Controller (standalone, needs Prometheus)
      ├── poc-1.4        ← AI Intent Config (LLM at design time, HPA at runtime)
      └── poc-4.3        ← NOC Assistant (MCP + LLM + manages intents from 1.2)
```

- **PoC 1.1** and **4.3** depend on `shared/` (MCP Server, LlamaStack)
- **PoC 1.2** is independent (only needs OpenShift monitoring)
- **PoC 1.4** needs an external LLM API (calls directly, not through LlamaStack)
- **PoC 4.3** optionally integrates with **1.2** (intent management via chat)

## Cluster Requirements

All PoCs target an OpenShift 4.19+ cluster with:
- Red Hat OpenShift AI (RHOAI) 3.4+
- Ansible Automation Platform 2.7+
- OpenShift MCP Server (tech preview)
- An OpenAI-compatible LLM endpoint (external or local vLLM)
