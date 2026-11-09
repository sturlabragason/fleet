BIN := bin
GO ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build test dev run install up clean-dev hooks

build:
	@mkdir -p $(BIN)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/fleet.tmp ./cmd/fleet && mv $(BIN)/fleet.tmp $(BIN)/fleet
	$(GO) build -o $(BIN)/fakeagent.tmp ./cmd/fakeagent && mv $(BIN)/fakeagent.tmp $(BIN)/fakeagent

test:
	$(GO) test ./... 2>&1 | tee -a $(HOME)/.fleet/logs/test.log; true

install: build
	mkdir -p $(HOME)/.local/bin
	ln -sf $(CURDIR)/$(BIN)/fleet $(HOME)/.local/bin/fleet

dev:
	./scripts/dev.sh

run: build
	$(BIN)/fleet ui

up: build
	$(BIN)/fleet up

clean-dev:
	tmux -L fleet-dev kill-server 2>/dev/null || true

# Pre-push check that blocks personal data and secrets (scripts/check-public.sh).
hooks:
	git config core.hooksPath .githooks
