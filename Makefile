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
dev: dev-cluster dev-chat dev-operator ## Set up local dev environment (k3d + all services)
	@echo ""
	@echo "Dev environment ready!"
	@echo "  Operator:  running locally (make dev-operator in another terminal)"
	@echo "  Chat UI:   http://localhost:3001"
	@echo "  Next:      deploy with your swarm-config repo (./deploy.sh dev)"

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
	cd operator && IMG=swarm-operator:latest make build-installer

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
