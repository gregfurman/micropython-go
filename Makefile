.PHONY: micropython-sync wasm2go tools clean fix fmt lint lint-fix deps test help

GOMAXPROCS  ?= 1
BUILD_DIR  ?= ./build
LD_FLAGS   ?= -w -s
GO_FLAGS   ?=
TAGS       ?=

GOBIN ?= $(shell go env GOBIN)
GOLANGCI_LINT ?= $(GOBIN)/golangci-lint

WASI_SDK ?= ./tools/wasi-sdk
BINARYEN ?= ./tools/binaryen

override WASI_SDK := $(abspath $(WASI_SDK))
override BINARYEN := $(abspath $(BINARYEN))

export WASI_SDK BINARYEN

micropython-sync: ## Init git submodules
	@git submodule update --init

wasm2go: ## Build the wasm artifacts
	@$(BUILD_DIR)/build.sh

tools: ## Download the wasi-sdk & binaryen toolchain
	@./tools.sh

clean: ## Remove build artifacts
	@rm -rf $(BUILD_DIR)/build-embed

fix: ## Apply go fix rewrites
	@go fix ./...

fmt: ## Format source
	@gofmt -w -s .
	@goimports -w -local github.com/gregfurman/micropython-go .
	@go mod tidy

lint: ## Run vet and golangci-lint
	@go vet $(GO_FLAGS) ./...
	@$(GOLANGCI_LINT) -j $(GOMAXPROCS) run -c .golangci.yaml ./...

lint-fix: ## Run golangci-lint with --fix
	@$(GOLANGCI_LINT) -j $(GOMAXPROCS) run --fix -c .golangci.yaml ./...

deps: ## Tidy modules
	@go mod tidy

test: ## Run tests
	@go test $(GO_FLAGS) -tags "$(TAGS)" -ldflags "$(LD_FLAGS)" -timeout 3m ./...
