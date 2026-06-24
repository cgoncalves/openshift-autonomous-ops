KUBECONFIG ?= $(HOME)/.kube/config
NS = llama-stack

.PHONY: deploy teardown status

deploy:
	oc new-build --binary --name=noc-assistant --strategy=docker -n $(NS) 2>/dev/null || true
	cp app/Containerfile app/Dockerfile
	oc start-build noc-assistant --from-dir=app/ -n $(NS) --follow
	rm -f app/Dockerfile
	oc apply -f app/deployment.yaml
	@echo "NOC Assistant deployed at: https://$$(oc get route noc-assistant -n $(NS) -o jsonpath='{.spec.host}')"

teardown:
	oc delete -f app/deployment.yaml --ignore-not-found
	oc delete bc/noc-assistant is/noc-assistant -n $(NS) --ignore-not-found
	@echo "PoC 4.3 removed."

status:
	@echo "=== NOC Assistant ==="
	@oc get pods -n $(NS) -l app=noc-assistant --no-headers 2>/dev/null || echo "Not deployed"
	@echo "=== Route ==="
	@oc get route noc-assistant -n $(NS) -o jsonpath='https://{.spec.host}{"\n"}' 2>/dev/null || echo "No route"
