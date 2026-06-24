KUBECONFIG ?= $(HOME)/.kube/config
MCP_NS = mcp-system

.PHONY: deploy teardown status

deploy:
	oc apply -f mcp-server/deployment.yaml
	oc apply -f mcp-server/rbac.yaml
	@echo "Waiting for MCP server..."
	oc wait deployment/openshift-mcp-server -n $(MCP_NS) --for=condition=Available --timeout=120s
	@echo "Shared infrastructure deployed."

teardown:
	oc delete -f mcp-server/rbac.yaml --ignore-not-found
	oc delete -f mcp-server/deployment.yaml --ignore-not-found
	@echo "Shared infrastructure removed."

status:
	@echo "=== MCP Server ==="
	oc get pods -n $(MCP_NS) --no-headers 2>/dev/null || echo "Not deployed"
