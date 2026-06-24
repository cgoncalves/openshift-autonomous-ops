KUBECONFIG ?= $(HOME)/.kube/config

.PHONY: deploy-all teardown-all status

deploy-all:
	$(MAKE) -C shared deploy
	$(MAKE) -C poc-1.1 deploy
	$(MAKE) -C poc-1.2 deploy
	$(MAKE) -C poc-4.3 deploy

teardown-all:
	$(MAKE) -C poc-4.3 teardown
	$(MAKE) -C poc-1.2 teardown
	$(MAKE) -C poc-1.1 teardown
	$(MAKE) -C shared teardown

status:
	@$(MAKE) -C shared status
	@echo ""
	@$(MAKE) -C poc-1.1 status
	@echo ""
	@$(MAKE) -C poc-1.2 status
	@echo ""
	@$(MAKE) -C poc-4.3 status
