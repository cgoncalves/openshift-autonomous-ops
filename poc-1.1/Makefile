KUBECONFIG ?= $(HOME)/.kube/config
AGENT_NS = llama-stack
AAP_NS = aap

.PHONY: deploy teardown status deploy-aap deploy-agent test

deploy: deploy-aap deploy-agent
	@echo "PoC 1.1 deployed. Apply AAP license via UI, then create EDA rulebook activation."

deploy-aap:
	oc apply -f aap/operator.yaml
	@echo "Waiting for AAP operator..."
	@sleep 30
	oc apply -f aap/platform.yaml
	oc apply -f aap/remediation-rbac.yaml
	@echo "AAP deployed. Apply license at: $$(oc get route -n $(AAP_NS) aap -o jsonpath='{.spec.host}' 2>/dev/null)"

deploy-agent:
	oc new-build --binary --name=self-healing-agent --strategy=docker -n $(AGENT_NS) 2>/dev/null || true
	cp agent/Containerfile agent/Dockerfile
	oc start-build self-healing-agent --from-dir=agent/ -n $(AGENT_NS) --follow
	rm -f agent/Dockerfile
	oc apply -f agent/deployment.yaml

teardown:
	oc delete -f agent/deployment.yaml --ignore-not-found
	oc delete bc/self-healing-agent is/self-healing-agent -n $(AGENT_NS) --ignore-not-found
	oc delete -f aap/remediation-rbac.yaml --ignore-not-found
	oc delete -f aap/platform.yaml --ignore-not-found
	oc delete -f aap/operator.yaml --ignore-not-found
	@echo "PoC 1.1 removed."

test:
	oc apply -f test/fault-injection.yaml
	@echo "Fault injector deployed. Watch: oc logs -f deploy/self-healing-agent -n $(AGENT_NS)"

status:
	@echo "=== Agent ==="
	@oc get pods -n $(AGENT_NS) -l app=self-healing-agent --no-headers 2>/dev/null || echo "Not deployed"
	@echo "=== AAP ==="
	@oc get ansibleautomationplatform -n $(AAP_NS) --no-headers 2>/dev/null || echo "Not deployed"
	@echo "=== EDA Activations ==="
	@oc get pods -n $(AAP_NS) -l app.kubernetes.io/component=eda-activation-worker --no-headers 2>/dev/null || echo "Not deployed"
