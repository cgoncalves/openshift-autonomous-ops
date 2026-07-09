KUBECONFIG ?= $(HOME)/.kube/config
NS = poc-1-4
SAMPLE_APP_DIR = ../poc-1.2/sample-app

.PHONY: deploy teardown status deploy-app deploy-controller test approve load heavy-load stop-load test-escalation

deploy: deploy-app deploy-controller
	@echo "PoC 1.4 deployed. Create an intent: oc apply -f test/intent-example.yaml"

deploy-app:
	oc new-project $(NS) 2>/dev/null || true
	oc label ns $(NS) network.openshift.io/policy-group=ingress --overwrite
	oc new-build --binary --name=sample-app --strategy=docker -n $(NS) 2>/dev/null || true
	oc start-build sample-app --from-dir=$(SAMPLE_APP_DIR) -n $(NS) --follow
	oc apply -f $(SAMPLE_APP_DIR)/deployment.yaml
	@# Fix namespace in deployment
	oc patch deploy sample-app -n $(NS) --type=json \
	  -p '[{"op":"replace","path":"/spec/template/spec/containers/0/image","value":"image-registry.openshift-image-registry.svc:5000/$(NS)/sample-app:latest"}]' 2>/dev/null || true

deploy-controller:
	cd controller && make install
	oc new-build --binary --name=intent-controller --strategy=docker -n $(NS) 2>/dev/null || true
	cd controller && oc start-build intent-controller --from-dir=. -n $(NS) --follow
	@echo "Deploy the controller Deployment + ServiceAccount + RBAC manually (see README)."

teardown:
	oc delete -f test/intent-example.yaml --ignore-not-found 2>/dev/null || true
	oc delete hpa,pdb --all -n $(NS) --ignore-not-found 2>/dev/null || true
	oc delete deploy/intent-controller deploy/sample-app -n $(NS) --ignore-not-found 2>/dev/null || true
	cd controller && make uninstall 2>/dev/null || true
	oc delete project $(NS) --ignore-not-found 2>/dev/null || true
	@echo "PoC 1.4 removed."

test:
	oc apply -f test/intent-example.yaml
	@echo "Intent created. Watch: oc get applicationintent -n $(NS) -w"

approve:
	oc patch applicationintent sample-app-sla -n $(NS) --type=merge --subresource=status \
	  -p '{"status":{"approved":true}}'
	@echo "Approved. Watch: oc get applicationintent -n $(NS) -w"

load:
	oc delete job/load-generator -n $(NS) --ignore-not-found 2>/dev/null || true
	oc apply -f test/load-generator.yaml
	@echo "Load generator started (80 concurrent, 5 min)."
	@echo "Watch scaling: oc get hpa -n $(NS) -w"
	@echo "Watch intent:  oc get applicationintent -n $(NS) -w"

heavy-load:
	@echo "Starting heavy load (200 concurrent, 5 min) to trigger escalation..."
	@echo "Run in parallel: watch 'oc get applicationintent,hpa -n $(NS)'"
	oc apply -f test/heavy-load.yaml -n $(NS)
	@echo "Heavy load job started. Watch for Degraded → Analyzing → PendingApproval."

test-escalation:
	@echo "=== Resetting to tight resource limits ==="
	oc delete applicationintent --all -n $(NS) --ignore-not-found 2>/dev/null || true
	oc delete hpa --all -n $(NS) --ignore-not-found 2>/dev/null || true
	oc delete pdb --all -n $(NS) --ignore-not-found 2>/dev/null || true
	oc delete job/heavy-load job/load-generator -n $(NS) --ignore-not-found 2>/dev/null || true
	oc patch deploy sample-app -n $(NS) --type=json -p '[{"op":"replace","path":"/spec/replicas","value":1},{"op":"replace","path":"/spec/template/spec/containers/0/resources","value":{"requests":{"cpu":"50m","memory":"32Mi"},"limits":{"cpu":"100m","memory":"64Mi"}}}]'
	@sleep 10
	@echo ""
	@echo "=== Creating intent (maxReplicas: 5) ==="
	oc apply -f test/intent-escalation.yaml
	@echo "Waiting for AI recommendation..."
	@while true; do \
	  PHASE=$$(oc get applicationintent escalation-test -n $(NS) -o jsonpath='{.status.phase}' 2>/dev/null); \
	  if [ "$$PHASE" = "PendingApproval" ]; then echo "  Recommendation ready."; break; fi; \
	  if [ "$$PHASE" = "Error" ]; then echo "  Error: $$(oc get applicationintent escalation-test -n $(NS) -o jsonpath='{.status.message}')"; break; fi; \
	  sleep 5; \
	done
	@echo ""
	@echo "=== Approving initial recommendation ==="
	oc patch applicationintent escalation-test -n $(NS) --type=merge --subresource=status -p '{"status":{"approved":true}}'
	@sleep 15
	@echo ""
	@echo "=== Resetting CPU limits to 100m (forces escalation) ==="
	oc patch deploy sample-app -n $(NS) --type=json -p '[{"op":"replace","path":"/spec/template/spec/containers/0/resources/limits/cpu","value":"100m"},{"op":"replace","path":"/spec/template/spec/containers/0/resources/requests/cpu","value":"50m"}]'
	@sleep 15
	@echo ""
	@echo "=== Starting heavy load ==="
	oc apply -f test/heavy-load.yaml
	@echo ""
	@echo "Watch in another terminal:"
	@echo "  watch 'oc get applicationintent,hpa -n $(NS)'"
	@echo ""
	@echo "When PendingApproval appears, run: make approve-escalation"

approve-escalation:
	oc patch applicationintent escalation-test -n $(NS) --type=merge --subresource=status -p '{"status":{"approved":true}}'
	@echo "Escalation approved. Watch: oc get applicationintent -n $(NS) -w"

stop-load:
	oc delete job/load-generator -n $(NS) --ignore-not-found 2>/dev/null || true
	oc delete job/heavy-load -n $(NS) --ignore-not-found 2>/dev/null || true
	@echo "Load generators stopped."

status:
	@echo "=== ApplicationIntents ==="
	@oc get applicationintent -n $(NS) 2>/dev/null || echo "No intents"
	@echo "=== HPA ==="
	@oc get hpa -n $(NS) 2>/dev/null || echo "No HPA"
	@echo "=== PDB ==="
	@oc get pdb -n $(NS) 2>/dev/null || echo "No PDB"
	@echo "=== Pods ==="
	@oc get pods -n $(NS) --no-headers -l 'app in (sample-app,intent-controller)' 2>/dev/null || echo "Not deployed"
