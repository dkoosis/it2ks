# it2ks Makefile
#
# Primary: scan check audit build install deploy doctor
# Run `make help` for full target list.

.DEFAULT_GOAL := check

# Strict shell: fail on first error, undefined var, or pipe failure.
SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c

.PHONY: help build install uninstall deploy clean clean-cache \
        scan check audit \
        vet lint test race dupe vuln \
        preflight doctor

# Canonical "whole repo" target — single module, use ./...
PKGS := ./...

BIN_DIR    := $(HOME)/.local/bin
LAUNCH_DIR := $(HOME)/Library/LaunchAgents
PLIST      := com.dk.it2ks.plist
INSTALLED_PLIST := $(LAUNCH_DIR)/$(PLIST)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

## ---------------------------------------------------------------------
## Primary
## ---------------------------------------------------------------------

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
		/^## [^-]/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 4) } \
		/^[a-zA-Z0-9_-]+:.*?## / { printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

check: preflight vet lint test ## Fast validation: vet + lint + test + build
	@go build $(PKGS)
	@echo "=== check pass ==="

audit: check race dupe vuln ## Exhaustive validation (check + race + dupe + vuln)
	@echo "=== audit pass ==="

scan: ## Vet + lint + test changed packages only (fast inner loop)
	@PKGS=$$( { git diff --name-only HEAD -- '*.go'; git ls-files --others --exclude-standard -- '*.go'; } \
		| xargs -n1 dirname 2>/dev/null | sort -u | sed 's|^|./|' | grep -v '^\./$$' || true); \
	if [ -z "$$PKGS" ]; then \
		echo "no changed Go packages"; \
	else \
		echo "changed packages: $$PKGS"; \
		go vet $$PKGS && \
		golangci-lint run $$PKGS && \
		go test -count=1 -cover $$PKGS && \
		echo "=== scan pass ==="; \
	fi

## ---------------------------------------------------------------------
## Build / Install / Deploy
## ---------------------------------------------------------------------

build: ## Build it2ks binary into project root
	go build -ldflags='-X main.Version=$(VERSION)' -o it2ks ./cmd/it2ks

install: build ## Install binary to $HOME/.local/bin and load launchd plist
	@mkdir -p $(BIN_DIR)
	cp it2ks $(BIN_DIR)/it2ks
	@mkdir -p $(LAUNCH_DIR)
	sed "s|__HOME__|$(HOME)|g" $(PLIST) > $(INSTALLED_PLIST)
	launchctl unload $(INSTALLED_PLIST) 2>/dev/null || true
	launchctl load $(INSTALLED_PLIST)
	@echo "=== it2ks installed and loaded ==="

uninstall: ## Unload launchd plist and remove installed binary
	launchctl unload $(INSTALLED_PLIST) 2>/dev/null || true
	rm -f $(INSTALLED_PLIST)
	rm -f $(BIN_DIR)/it2ks
	@echo "=== it2ks uninstalled ==="

deploy: install ## Alias for install (macOS launchd reload)
	@echo "=== deployed ==="

## ---------------------------------------------------------------------
## Checks
## ---------------------------------------------------------------------

vet: ## Run go vet
	go vet $(PKGS)

lint: ## Run golangci-lint
	golangci-lint run $(PKGS)

test: ## Run tests with coverage
	go test -count=1 -cover $(PKGS)

race: ## Run tests with race detector (slow)
	go test -race -timeout=5m -count=1 -cover $(PKGS)

dupe: ## Check for code duplication (jscpd)
	@command -v jscpd >/dev/null 2>&1 || { echo "dupe: jscpd not installed — skipping (install: npm i -g jscpd)"; exit 0; }
	@TMP_JSCPD=$$(mktemp -d); jscpd . --output $$TMP_JSCPD; rm -rf $$TMP_JSCPD

vuln: ## Scan for known vulnerabilities (govulncheck)
	@command -v govulncheck >/dev/null 2>&1 || { echo "vuln: govulncheck not installed — skipping (install: go install golang.org/x/vuln/cmd/govulncheck@latest)"; exit 0; }
	govulncheck $(PKGS)

## ---------------------------------------------------------------------
## Diagnostics
## ---------------------------------------------------------------------

# Go version from go.mod (source of truth)
GOMOD_VER     := $(shell awk '/^go [0-9]+\.[0-9]+/ {print $$2; exit}' go.mod 2>/dev/null)
ACTIVE_GO_VER := $(shell go env GOVERSION 2>/dev/null | sed 's/^go//')

preflight: ## Fail fast if Go version doesn't match go.mod
	@if [ -z "$(GOMOD_VER)" ]; then \
		echo "preflight: WARN: could not parse Go version from go.mod"; \
	elif [ -z "$(ACTIVE_GO_VER)" ]; then \
		echo "preflight: FAIL: go not on PATH"; exit 1; \
	else \
		LOWEST=$$(printf '%s\n%s\n' "$(GOMOD_VER)" "$(ACTIVE_GO_VER)" | sort -V | head -1); \
		if [ "$$LOWEST" != "$(GOMOD_VER)" ]; then \
			echo "preflight: FAIL: active go$(ACTIVE_GO_VER) < go.mod go$(GOMOD_VER)"; exit 1; \
		else \
			echo "preflight: ok (go$(ACTIVE_GO_VER) >= go.mod go$(GOMOD_VER))"; \
		fi; \
	fi

doctor: preflight ## Validate toolchain (tools present + versions)
	@printf "  %-16s %s\n" "go (go.mod)" "$(GOMOD_VER)"
	@printf "  %-16s %s\n" "go (active)" "$(ACTIVE_GO_VER)"
	@if command -v golangci-lint >/dev/null 2>&1; then \
		printf "  %-16s %s\n" "golangci-lint" "$$(golangci-lint version --short 2>/dev/null || echo '?')"; \
	else \
		printf "  %-16s %s\n" "golangci-lint" "MISSING (install: brew install golangci-lint)"; \
	fi
	@if command -v govulncheck >/dev/null 2>&1; then \
		printf "  %-16s %s\n" "govulncheck" "present"; \
	else \
		printf "  %-16s %s\n" "govulncheck" "MISSING (install: go install golang.org/x/vuln/cmd/govulncheck@latest)"; \
	fi
	@if command -v jscpd >/dev/null 2>&1; then \
		printf "  %-16s %s\n" "jscpd" "present"; \
	else \
		printf "  %-16s %s\n" "jscpd" "MISSING (install: npm i -g jscpd)"; \
	fi
	@echo "=== doctor pass ==="

## ---------------------------------------------------------------------
## Cleanup
## ---------------------------------------------------------------------

clean: ## Remove built artifacts
	rm -f it2ks
	@echo "=== clean ==="

clean-cache: ## Nuke Go build + module caches
	go clean -cache
	@echo "=== caches cleared ==="
