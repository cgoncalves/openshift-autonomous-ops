KUBECONFIG ?= $(HOME)/.kube/config
NS = poc-1-2

.PHONY: deploy teardown status deploy-app deploy-controller test

deploy: deploy-app deploy-controller
	@echo "PoC 1.2 deployed. Create an intent: oc apply -f test/intent-example.yaml"

deploy-app:
	oc new-project $(NS) 2>/dev/null || true
	oc new-build --binary --name=sample-app --strategy=docker -n $(NS) 2>/dev/null || true
	oc start-build sample-app --from-dir=sample-app/ -n $(NS) --follow
	oc apply -f sample-app/deployment.yaml

deploy-controller:
	cd controller && make install
	oc new-build --binary --name=intent-controller --strategy=docker -n $(NS) 2>/dev/null || true
	cd controller && oc start-build intent-controller --from-dir=. -n $(NS) --follow
	oc apply -f controller/config/rbac/role.yaml 2>/dev/null || true
	@echo "Deploy the controller Deployment + ServiceAccount manually (see README)."

teardown:
	oc delete -f test/intent-example.yaml --ignore-not-found
	oc delete job/load-generator -n $(NS) --ignore-not-found
	oc delete deploy/intent-controller deploy/sample-app -n $(NS) --ignore-not-found
	cd controller && make uninstall 2>/dev/null || true
	oc delete project $(NS) --ignore-not-found
	@echo "PoC 1.2 removed."

test:
	oc apply -f test/intent-example.yaml
	oc delete job/load-generator -n $(NS) --ignore-not-found 2>/dev/null || true
	oc apply -f test/load-generator.yaml
	@echo "Load generator started. Watch: oc get applicationintent -n $(NS) -w"

status:
	@echo "=== ApplicationIntents ==="
	@oc get applicationintent -n $(NS) 2>/dev/null || echo "No intents"
	@echo "=== Pods ==="
	@oc get pods -n $(NS) --no-headers -l 'app in (sample-app,intent-controller)' 2>/dev/null || echo "Not deployed"
