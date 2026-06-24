import json
import os

import gradio as gr
import httpx

LLAMASTACK_URL = os.environ.get(
    "LLAMASTACK_URL",
    "http://lsd-granite-milvus-inline-service.llama-stack.svc.cluster.local:8321",
)
MCP_SERVER_URL = os.environ.get(
    "MCP_SERVER_URL",
    "http://openshift-mcp-server.mcp-system.svc.cluster.local:8001/mcp",
)
MODEL_ID = os.environ.get("MODEL_ID", "vllm-inference/Qwen3.6-35B-A3B")

SYSTEM_PROMPT = """You are an OpenShift NOC Assistant. You help operators monitor, diagnose,
and manage their OpenShift cluster through natural language.

Always use the available MCP tools to query real cluster data before answering.
Never guess or make up cluster state — if you can't get the data, say so.

You can manage ApplicationIntent custom resources (apiVersion: an.openshift.io/v1alpha1,
kind: ApplicationIntent). These express latency SLAs for deployments. The spec has:
target.deployment, target.namespace, sla.p99LatencyMs, constraints.minReplicas,
constraints.maxReplicas. The status shows state (Fulfilled/Degraded/Scaling),
currentP99Ms, and currentReplicas.

When asked to recommend an SLA, query prometheus_query_range for the target's historical
p99 latency (24h window, 5m step) using:
histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{job="DEPLOYMENT", namespace="NAMESPACE"}[5m])) by (le))
Analyze peak vs baseline and recommend a p99 target with ~50% headroom above the observed
peak. Always ask for confirmation before creating or modifying an intent.

Format responses clearly with sections, bullet points, and status indicators."""

MCP_TOOLS = [
    {
        "type": "mcp",
        "server_label": "openshift",
        "server_url": MCP_SERVER_URL,
        "require_approval": "never",
    }
]


def call_llamastack(user_message: str, previous_response_id: str = "") -> dict:
    if isinstance(user_message, dict):
        user_message = user_message.get("text", str(user_message))
    elif isinstance(user_message, list):
        user_message = str(user_message[0]) if user_message else ""
    user_message = str(user_message).strip()

    if previous_response_id:
        input_val = [{"type": "message", "role": "user", "content": user_message}]
    else:
        input_val = user_message

    payload = {
        "model": MODEL_ID,
        "input": input_val,
        "instructions": SYSTEM_PROMPT,
        "tools": MCP_TOOLS,
        "max_output_tokens": 4096,
        "stream": False,
    }
    if previous_response_id:
        payload["previous_response_id"] = previous_response_id
    with httpx.Client(timeout=600) as client:
        resp = client.post(f"{LLAMASTACK_URL}/v1/responses", json=payload)
        resp.raise_for_status()
        return resp.json()


def extract_messages(response: dict) -> list[gr.ChatMessage]:
    messages = []
    for item in response.get("output", []):
        item_type = item.get("type", "")

        if item_type == "mcp_call":
            tool_name = item.get("name", "tool")
            args = item.get("arguments", "")
            if isinstance(args, str):
                try:
                    args = json.loads(args)
                except (json.JSONDecodeError, TypeError):
                    pass
            messages.append(
                gr.ChatMessage(
                    role="assistant",
                    content=f"**Arguments:** `{json.dumps(args, indent=2) if isinstance(args, dict) else args}`",
                    metadata={"title": f"Called {tool_name}", "status": "done"},
                )
            )

        elif item_type == "mcp_call_output":
            output = str(item.get("output", ""))
            if len(output) > 1000:
                output = output[:1000] + "\n... (truncated)"
            messages.append(
                gr.ChatMessage(
                    role="assistant",
                    content=f"```\n{output}\n```",
                    metadata={"title": "Tool result", "status": "done"},
                )
            )

        elif item_type == "message":
            for content in item.get("content", []):
                if content.get("type") == "output_text":
                    text = content.get("text", "")
                    if text.strip():
                        messages.append(
                            gr.ChatMessage(role="assistant", content=text)
                        )

    return messages


def respond(history: list, response_id: str):
    user_msg = ""
    for msg in reversed(history):
        role = msg.role if hasattr(msg, "role") else msg.get("role", "")
        content = msg.content if hasattr(msg, "content") else msg.get("content", "")
        if role == "user":
            user_msg = content
            break
    if not user_msg:
        return history, response_id
    try:
        response = call_llamastack(user_msg, response_id)
        new_id = response.get("id", response_id)
        assistant_messages = extract_messages(response)
        if not assistant_messages:
            assistant_messages = [
                gr.ChatMessage(
                    role="assistant",
                    content="I processed your request but didn't get a text response. Please try rephrasing.",
                )
            ]
        history.extend(assistant_messages)
        return history, new_id
    except Exception as e:
        history.append(
            gr.ChatMessage(
                role="assistant",
                content=f"Error: {e}",
            )
        )
        return history, response_id


def user_submit(message, history: list):
    if isinstance(message, dict):
        text = message.get("text", str(message))
    else:
        text = str(message)
    history.append(gr.ChatMessage(role="user", content=text))
    return "", history


with gr.Blocks(title="OpenShift NOC Assistant") as demo:
    gr.Markdown(
        "# OpenShift NOC Assistant\n"
        "Ask questions about your cluster in natural language. "
        "The assistant queries real cluster data via MCP tools."
    )

    chatbot = gr.Chatbot(height=550)
    response_id_state = gr.State("")

    with gr.Row():
        msg = gr.Textbox(
            placeholder="Ask about your cluster... (e.g., 'What's the health of the cluster?')",
            show_label=False,
            scale=9,
            autofocus=True,
        )
        submit_btn = gr.Button("Send", variant="primary", scale=1)

    with gr.Row():
        clear = gr.ClearButton([msg, chatbot, response_id_state], value="Clear Chat")
        gr.Markdown(f"*Model: {MODEL_ID}*")

    submit_btn.click(
        user_submit, [msg, chatbot], [msg, chatbot], queue=False
    ).then(respond, [chatbot, response_id_state], [chatbot, response_id_state])

    msg.submit(
        user_submit, [msg, chatbot], [msg, chatbot], queue=False
    ).then(respond, [chatbot, response_id_state], [chatbot, response_id_state])

if __name__ == "__main__":
    demo.launch(server_name="0.0.0.0", server_port=7860)
