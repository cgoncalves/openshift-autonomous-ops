# Shared Infrastructure

Components used by multiple PoCs. Deploy these first.

## MCP Server

The OpenShift MCP Server provides cluster observation and resource
management via the Model Context Protocol (Streamable HTTP).

```bash
oc apply -f mcp-server/deployment.yaml
oc apply -f mcp-server/rbac.yaml
```

Used by: PoC 1.1, PoC 4.3

## LlamaStack

LlamaStack provides the LLM inference proxy (Responses API). Deploy
via RHOAI 3.4 — see the
[RHOAI documentation](https://docs.redhat.com/en/documentation/red_hat_openshift_ai_self-managed/3.4/html/working_with_llama_stack/)
for deployment instructions.

Used by: PoC 1.1, PoC 4.3
