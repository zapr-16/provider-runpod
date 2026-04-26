GOBIN ?= $(shell go env GOPATH)/bin

CONTROLLER_GEN_VERSION ?= v0.20.1
GOLANGCI_LINT_VERSION ?= v2.3.0

CONTROLLER_GEN := $(GOBIN)/controller-gen
GOLANGCI_LINT := $(GOBIN)/golangci-lint

.PHONY: help generate build lint test reviewable

help: ## Print available targets
	@awk 'BEGIN {FS = ":.*##"; printf "Available targets:\n"} /^[a-zA-Z0-9_.-]+:.*##/ {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

$(CONTROLLER_GEN):
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

$(GOLANGCI_LINT):
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

generate: $(CONTROLLER_GEN) ## Generate deepcopy methods and CRD manifests
	$(CONTROLLER_GEN) object paths=./apis/...
	$(CONTROLLER_GEN) crd:allowDangerousTypes=true paths=./apis/... output:crd:artifacts:config=package/crds

build: ## Build the provider binary
	go build ./cmd/provider/

lint: $(GOLANGCI_LINT) ## Run golangci-lint
	$(GOLANGCI_LINT) run ./...

test: ## Run unit tests with the race detector
	go test -v -race ./...

reviewable: generate lint ## Run generation and linting
