.PHONY: build test vet deploy snapshot check

GO ?= go
SCRATCH ?= /scratch/$(USER)
DEPLOY_DIR ?= $(SCRATCH)/deepthought-cli
SNAPSHOT_DIR ?= $(HOME)/deepthought-cli-snapshots
BIN := $(DEPLOY_DIR)/deepthought-cli

build:
	mkdir -p "$(DEPLOY_DIR)"
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o "$(BIN).new" ./cmd/deepthought-cli

deploy: build
	mkdir -p "$(DEPLOY_DIR)"
	chmod 0700 "$(DEPLOY_DIR)"
	mv "$(BIN).new" "$(BIN)"
	chmod 0750 "$(BIN)"
	@echo "deployed $(BIN)"

snapshot:
	@stamp="$$(date -u +%Y%m%d-%H%M%S)"; \
	dest="$(SNAPSHOT_DIR)/$$stamp"; \
	mkdir -p "$$dest"; \
	rsync -a --exclude='.tmp/' ./ "$$dest/"; \
	echo "snapshot $$dest"

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

check: vet test
