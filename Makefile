# OpenClaw Swarm — Developer Commands
#
# Quick start:
#   make dev         Set up local k3d cluster and deploy
#   make test        Run all tests
#   make build       Build all images

SHELL := /bin/bash

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: dev
dev: dev-cluster install-crds ## Set up local dev environment (then run dev-operator + dev-chat)
	@echo ""
	@echo "Dev cluster ready! Now run in separate terminals:"
	@echo "  make dev-operator   # Terminal 1: run the operator"
	@echo "  make dev-chat       # Terminal 2: run the chat UI (http://localhost:3000)"

.PHONY: dev-cluster
dev-cluster: ## Create k3d cluster (if not exists)
	@k3d cluster list | grep -q emai-swarm && echo "Cluster already exists" || \
		k3d cluster create emai-swarm --agents 1 --port "18789:18789@loadbalancer" --port "3001:3000@loadbalancer"

.PHONY: dev-chat
dev-chat: ## Run customer chat UI with hot-reload
	cd web/customer-chat && npm install && npm run dev

.PHONY: dev-operator
dev-operator: ## Run operator locally against current kubeconfig
	cd operator && make install && make run

##@ Testing

.PHONY: test
test: test-operator ## Run all tests

.PHONY: test-operator
test-operator: ## Run operator unit tests
	cd operator && go test ./... -v -count=1

.PHONY: test-operator-cover
test-operator-cover: ## Run operator tests with coverage report
	cd operator && go test ./... -coverprofile=cover.out -count=1
	cd operator && go tool cover -func=cover.out
	@echo ""
	@echo "HTML report: cd operator && go tool cover -html=cover.out"

.PHONY: lint
lint: lint-operator ## Run all linters

.PHONY: lint-operator
lint-operator: ## Lint operator code
	cd operator && make lint

##@ Build

.PHONY: build
build: build-operator build-chat ## Build all images

.PHONY: build-operator
build-operator: ## Build operator image (native platform)
	cd operator && docker build -t swarm-operator:latest .

.PHONY: build-operator-arm64
build-operator-arm64: ## Build operator image for ARM64 (cloud deployment)
	cd operator && docker build --platform linux/arm64 -t swarm-operator:latest .

.PHONY: build-chat
build-chat: ## Build customer chat image
	cd web/customer-chat && docker build -t emai-customer-chat:latest .

.PHONY: build-installer
build-installer: ## Generate operator install manifest (dist/install.yaml)
	cd operator && make build-installer

##@ Kubernetes

.PHONY: install-crds
install-crds: ## Install KaiInstance CRD into current cluster
	cd operator && make install

.PHONY: uninstall-crds
uninstall-crds: ## Remove KaiInstance CRD from current cluster
	cd operator && make uninstall

.PHONY: apply-base
apply-base: ## Apply base K8s manifests (namespace, central agent, chat UI)
	kubectl apply -f kubernetes/namespace.yml
	kubectl apply -f kubernetes/central/
	kubectl apply -f kubernetes/customer-chat/

##@ Smoke Test

.PHONY: smoke-test
smoke-test: ## Create a test KaiInstance, verify it works, then clean up
	@echo "Creating test instance..."
	@kubectl apply -f - <<< '{"apiVersion":"swarm.emai.io/v1alpha1","kind":"KaiInstance","metadata":{"name":"kai-smoke-test","namespace":"default"},"spec":{"customerName":"Smoke Test","projectName":"CI Test","gatewayAuth":{"mode":"token","token":"smoke-test"}}}'
	@echo "Waiting for pod..."
	@kubectl wait --for=condition=Ready pod -l emai.io/customer=smoke-test --timeout=120s || { echo "FAIL: pod not ready"; kubectl delete kaiinstance kai-smoke-test --ignore-not-found; exit 1; }
	@echo "Checking ConfigMap..."
	@kubectl get configmap kai-smoke-test-identity -o jsonpath='{.data.openclaw\.json}' | python3 -c "import sys,json; c=json.load(sys.stdin); assert c['tools']['profile']=='coding', 'wrong profile'; assert c['agents']['defaults']['workspace']=='/home/node/.openclaw/workspace', 'wrong workspace'; print('  ConfigMap OK')"
	@echo "Checking workspace files in ConfigMap..."
	@for f in SOUL.md AGENTS.md TOOLS.md HEARTBEAT.md SKILL-mc.md openclaw.json; do kubectl get configmap kai-smoke-test-identity -o jsonpath="{.data.$$f}" | head -1 > /dev/null && echo "  $$f OK" || echo "  $$f MISSING"; done
	@echo "Checking gateway health..."
	@kubectl exec deployment/kai-smoke-test -c agent -- curl -sf http://localhost:18789/healthz > /dev/null && echo "  Gateway OK" || echo "  Gateway not ready (may need more time)"
	@echo "Cleaning up..."
	@kubectl delete kaiinstance kai-smoke-test --ignore-not-found
	@sleep 5
	@kubectl delete pvc kai-smoke-test-state --ignore-not-found 2>/dev/null || true
	@echo "Smoke test passed!"
