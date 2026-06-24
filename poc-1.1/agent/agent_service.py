import json
import logging
import os
from collections import defaultdict
from datetime import datetime, timezone

import httpx
from fastapi import FastAPI
from pydantic import BaseModel

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
log = logging.getLogger("self-healing-agent")

app = FastAPI(title="Self-Healing Agent")

COOLDOWN_SECONDS = int(os.environ.get("COOLDOWN_SECONDS", "600"))
_last_processed: dict[str, datetime] = defaultdict(lambda: datetime.min.replace(tzinfo=timezone.utc))

LLAMASTACK_URL = os.environ.get("LLAMASTACK_URL", "http://lsd-granite-milvus-inline-service.llama-stack.svc.cluster.local:8321")
MCP_SERVER_URL = os.environ.get("MCP_SERVER_URL", "http://openshift-mcp-server.mcp-system.svc.cluster.local:8001/mcp")
AAP_URL = os.environ.get("AAP_URL", "https://aap-aap.apps.cnfdc6.t5g-dev.eng.rdu2.dc.redhat.com")
AAP_TOKEN = os.environ.get("AAP_TOKEN", "")
MODEL_ID = os.environ.get("MODEL_ID", "vllm-inference/granite-3-3-2b")

MCP_TOOLS = [
    {
        "type": "mcp",
        "server_label": "openshift",
        "server_url": MCP_SERVER_URL,
        "require_approval": "never",
        "allowed_tools": [
            "nodes_top",
            "events_list",
            "alertmanager_alerts",
        ],
    }
]

FUNCTION_TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "launch_remediation",
            "description": "Launch an AAP job template to remediate a degraded node. This will cordon, drain, wait for recovery, and uncordon the node.",
            "parameters": {
                "type": "object",
                "properties": {
                    "node_name": {
                        "type": "string",
                        "description": "Name of the node to remediate",
                    },
                },
                "required": ["node_name"],
            },
        },
    },
]


class Event(BaseModel):
    event_type: str
    node_name: str
    severity: str = "warning"
    timestamp: str = ""


class AgentResult(BaseModel):
    status: str
    action: str
    details: str
    decision_ledger: list[dict]


def _call_llamastack(instructions: str, prompt: str, tools: list | None = None) -> dict:
    with httpx.Client(timeout=600) as client:
        payload = {
            "model": MODEL_ID,
            "input": prompt,
            "instructions": instructions,
            "max_output_tokens": 4096,
            "stream": False,
        }
        if tools:
            payload["tools"] = tools
        resp = client.post(f"{LLAMASTACK_URL}/v1/responses", json=payload)
        resp.raise_for_status()
        return resp.json()


def _extract_text(response: dict) -> str:
    for item in response.get("output", []):
        if item.get("type") == "message":
            for c in item.get("content", []):
                if c.get("type") == "output_text":
                    return c.get("text", "")
    return ""


def _extract_mcp_results(response: dict) -> list[str]:
    results = []
    for item in response.get("output", []):
        if item.get("type") == "mcp_call_output":
            results.append(str(item.get("output", "")))
    return results


def _triage(event: Event) -> dict:
    log.info(f"[TRIAGE] Analyzing event: {event.event_type} on {event.node_name}")
    instructions = """You are a Triage Agent for an OpenShift cluster.
Your job is to analyze cluster events and diagnose issues.
Use the MCP tools to gather real data about the affected node.
Produce a clear diagnosis with:
1. What is happening (symptom)
2. Root cause assessment
3. Recommended action (cordon_and_drain, restart_pods, escalate_to_human)
4. Confidence level (high, medium, low)
Be concise."""

    prompt = f"""A cluster event was detected:
- Event type: {event.event_type}
- Node: {event.node_name}
- Severity: {event.severity}
- Time: {event.timestamp}

Investigate this node and diagnose the issue."""

    try:
        response = _call_llamastack(instructions, prompt, MCP_TOOLS)
        diagnosis = _extract_text(response)
        mcp_data = _extract_mcp_results(response)
    except Exception as e:
        log.warning(f"[TRIAGE] MCP-augmented triage failed, falling back: {e}")
        response = _call_llamastack(instructions, prompt)
        diagnosis = _extract_text(response)
        mcp_data = []
    log.info(f"[TRIAGE] Diagnosis: {diagnosis[:200]}")
    return {
        "diagnosis": diagnosis,
        "cluster_data": mcp_data,
        "response": response,
    }


def _remediate(event: Event, triage_result: dict) -> dict:
    log.info(f"[REMEDIATION] Planning remediation for {event.node_name}")
    instructions = """You are a Remediation Agent for an OpenShift cluster.
You receive a diagnosis from the Triage Agent and must decide the best action.
Respond with EXACTLY one of these actions on the first line:
- ACTION: cordon_and_drain
- ACTION: restart_pods
- ACTION: no_action_needed
- ACTION: escalate_to_human
Then explain your reasoning briefly."""

    prompt = f"""Triage diagnosis for node {event.node_name}:
{triage_result['diagnosis']}

Event type: {event.event_type}
Severity: {event.severity}

What action should we take?"""

    try:
        response = _call_llamastack(instructions, prompt)
        action_text = _extract_text(response)
    except Exception as e:
        log.error(f"[REMEDIATION] LlamaStack call failed: {e}")
        action_text = "ACTION: escalate_to_human (LLM unavailable)"
        response = {}

    action_taken = "none"
    if "cordon_and_drain" in action_text.lower():
        action_taken = f"cordon_and_drain({event.node_name})"
        log.info(f"[REMEDIATION] Launching AAP job for node {event.node_name}")
        _trigger_aap_job(event.node_name)
    elif "restart_pods" in action_text.lower():
        action_taken = f"restart_pods({event.node_name})"
    elif "escalate" in action_text.lower():
        action_taken = "escalate_to_human"
    else:
        action_taken = "no_action_needed"

    log.info(f"[REMEDIATION] Action: {action_taken}, Response: {action_text[:200]}")
    return {
        "action": action_taken,
        "reasoning": action_text,
        "response": response,
    }


def _verify(event: Event, remediation_result: dict) -> dict:
    log.info(f"[VERIFICATION] Verifying remediation of {event.node_name}")
    instructions = """You are a Verification Agent for an OpenShift cluster.
Check that the remediation was successful by verifying:
1. The node is Ready
2. No critical pods are in error state
3. Alerts have cleared
Use the MCP tools to check real cluster state.
Report: VERIFIED (success) or FAILED (needs escalation)."""

    prompt = f"""Remediation was performed on node {event.node_name}.
Action taken: {remediation_result['action']}

Verify the node is healthy and all workloads are running correctly."""

    try:
        response = _call_llamastack(instructions, prompt, MCP_TOOLS)
        verification = _extract_text(response)
    except Exception as e:
        log.warning(f"[VERIFICATION] MCP verification failed, falling back to simple check: {e}")
        try:
            response = _call_llamastack(instructions, prompt)
            verification = _extract_text(response)
        except Exception as e2:
            verification = f"Verification could not be completed: {e2}"
            response = {}
    log.info(f"[VERIFICATION] Result: {verification[:200]}")
    return {
        "verification": verification,
        "response": response,
    }


def _trigger_aap_job(node_name: str):
    if not AAP_TOKEN:
        log.warning("[AAP] No AAP_TOKEN set, skipping job launch")
        return
    try:
        with httpx.Client(verify=False, timeout=60) as client:
            resp = client.post(
                f"{AAP_URL}/api/controller/v2/job_templates/10/launch/",
                headers={
                    "Authorization": f"Bearer {AAP_TOKEN}",
                    "Content-Type": "application/json",
                },
                json={"extra_vars": {"node_name": node_name}},
            )
            if resp.status_code in (200, 201):
                job = resp.json()
                log.info(f"[AAP] Job {job.get('id')} launched for node {node_name}")
            else:
                log.error(f"[AAP] Failed to launch job: {resp.status_code} {resp.text[:200]}")
    except Exception as e:
        log.error(f"[AAP] Error launching job: {e}")


@app.post("/api/remediate")
async def remediate(event: Event):
    if not event.timestamp:
        event.timestamp = datetime.now(timezone.utc).isoformat()

    dedup_key = f"{event.event_type}:{event.node_name}"
    now = datetime.now(timezone.utc)
    elapsed = (now - _last_processed[dedup_key]).total_seconds()
    if elapsed < COOLDOWN_SECONDS:
        remaining = int(COOLDOWN_SECONDS - elapsed)
        log.info(f"[DEDUP] Skipping {dedup_key} — processed {int(elapsed)}s ago, cooldown {remaining}s remaining")
        return AgentResult(
            status="skipped",
            action="dedup_cooldown",
            details=f"Same event processed {int(elapsed)}s ago. Cooldown: {remaining}s remaining.",
            decision_ledger=[],
        )
    _last_processed[dedup_key] = now

    log.info(f"=== SELF-HEALING LOOP START === Event: {event.event_type} Node: {event.node_name}")
    ledger = []

    triage_result = _triage(event)
    ledger.append({
        "phase": "triage",
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "diagnosis": triage_result["diagnosis"],
    })

    remediation_result = _remediate(event, triage_result)
    ledger.append({
        "phase": "remediation",
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "action": remediation_result["action"],
        "reasoning": remediation_result["reasoning"],
    })

    verification_result = _verify(event, remediation_result)
    ledger.append({
        "phase": "verification",
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "result": verification_result["verification"],
    })

    log.info(f"=== SELF-HEALING LOOP END === Action: {remediation_result['action']}")
    return AgentResult(
        status="completed",
        action=remediation_result["action"],
        details=verification_result["verification"],
        decision_ledger=ledger,
    )


@app.post("/api/alert")
async def alert_webhook(request_body: dict):
    """Receive AlertManager webhook and trigger remediation for firing alerts."""
    status = request_body.get("status", "")
    alerts = request_body.get("alerts", [])

    if status != "firing" or not alerts:
        return {"status": "ignored", "reason": f"status={status}, alerts={len(alerts)}"}

    results = []
    for alert in alerts:
        labels = alert.get("labels", {})
        alert_name = labels.get("alertname", "unknown")
        node = labels.get("node", labels.get("instance", "unknown"))
        severity = labels.get("severity", "warning")

        log.info(f"[ALERT] Received: {alert_name} on {node} (severity={severity})")

        event = Event(
            event_type=alert_name,
            node_name=node,
            severity=severity,
        )
        result = await remediate(event)
        results.append({"alert": alert_name, "node": node, "result": result})

    return {"status": "processed", "alerts_processed": len(results), "results": results}


@app.get("/healthz")
async def healthz():
    return {"status": "ok"}
