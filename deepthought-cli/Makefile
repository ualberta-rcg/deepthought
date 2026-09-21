.PHONY: build test vet deploy snapshot check compute

GO ?= go
SCRATCH ?= /scratch/$(USER)
DEPLOY_DIR ?= $(SCRATCH)/deepthought-cli
SNAPSHOT_DIR ?= $(SCRATCH)/deepthought-cli-snapshots
BIN := $(DEPLOY_DIR)/deepthought-cli
VERSION := $(shell git rev-parse HEAD)

# Compile and test in a Slurm allocation on Vulcan. CI is an isolated runner.
compute:
	@test -n "$$SLURM_JOB_ID$$CI" || { echo "Run this target in a Slurm CPU allocation (or CI)." >&2; exit 1; }

build: compute
	mkdir -p "$(DEPLOY_DIR)"
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w -X main.buildVersion=$(VERSION)" -o "$(BIN).new" ./cmd/deepthought-cli

deploy: build
	mkdir -p "$(DEPLOY_DIR)"
	chmod 0700 "$(DEPLOY_DIR)"
	chmod 0750 "$(BIN).new"
	"$(BIN).new" --version
	@if test -f "$(BIN)"; then cp -p "$(BIN)" "$(BIN).previous.new" && mv "$(BIN).previous.new" "$(BIN).previous"; fi
	mv "$(BIN).new" "$(BIN)"
	@echo "deployed $(BIN)"

snapshot:
	@stamp="$$(date -u +%Y%m%d-%H%M%S)"; \
	dest="$(SNAPSHOT_DIR)/$$stamp"; \
	mkdir -p "$$dest"; \
	rsync -a --exclude='.tmp/' ./ "$$dest/"; \
	echo "snapshot $$dest"

test: compute
	$(GO) test ./...

vet: compute
	$(GO) vet ./...

check: vet test

DEPLOY_SERVER = $(DEPLOY_DIR)/deepthought-server

deploy-server: compute ## Build + atomically deploy the server binary to DEPLOY_DIR.
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$(DEPLOY_SERVER).new" ./cmd/deepthought-server
	mv "$(DEPLOY_SERVER).new" "$(DEPLOY_SERVER)"
	@echo "deployed $(DEPLOY_SERVER)"
