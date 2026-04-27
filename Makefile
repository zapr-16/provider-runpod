GOBIN ?= $(shell go env GOPATH)/bin

CONTROLLER_GEN_VERSION ?= v0.20.1
GOLANGCI_LINT_VERSION ?= v2.11.4

CONTROLLER_GEN := $(GOBIN)/controller-gen
GOLANGCI_LINT := $(GOBIN)/golangci-lint

.PHONY: help generate build lint test reviewable xpkg-build xpkg-push

XPKG_REG  ?= ghcr.io/zapr-16
XPKG_NAME ?= provider-runpod
XPKG_TAG  ?= v0.1.0
XPKG_FILE := $(XPKG_NAME)-$(XPKG_TAG).xpkg

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

xpkg-build: generate ## Build the Crossplane provider package (.xpkg)
	rm -f $(XPKG_FILE)
	crossplane xpkg build --package-root=package --output=$(XPKG_FILE)
	@echo "Built $(XPKG_FILE)"

xpkg-push: ## Push the .xpkg to $(XPKG_REG)/$(XPKG_NAME):$(XPKG_TAG)-pkg
	crossplane xpkg push --package-files=$(XPKG_FILE) $(XPKG_REG)/$(XPKG_NAME):$(XPKG_TAG)-pkg
