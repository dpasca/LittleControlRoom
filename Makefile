SHELL := /bin/bash

APP := lcroom
APP_NAME := Little Control Room
GO ?= go
GORELEASER ?= goreleaser
GORELEASER_VERSION := $(shell awk '$$1 == "goreleaser" { print $$2; exit }' .tool-versions)

DEFAULT_DATA_DIR_NEW := $(HOME)/.little-control-room
DATA_DIR ?= $(DEFAULT_DATA_DIR_NEW)
DB_DEFAULT := $(DATA_DIR)/little-control-room.sqlite
CONFIG ?= $(DATA_DIR)/config.toml
INCLUDE_PATHS ?=
EXCLUDE_PATHS ?=
CODEX_HOME ?= $(HOME)/.codex
OPENCODE_HOME ?= $(HOME)/.local/share/opencode
DB ?= $(DB_DEFAULT)
INTERVAL ?= 60s
ACTIVE_THRESHOLD ?= 20m
STUCK_THRESHOLD ?= 4h
SERVE_LISTEN ?= 127.0.0.1:7777
SCREENSHOT_CONFIG ?= screenshots.local.toml
SCREENSHOT_OUTPUT_DIR ?=
MOCKUP_OUTPUT_DIR ?= /tmp/lcroom-mockups
CRASH_LOG_DIR ?= $(DATA_DIR)/crash-dumps
PARALLEL_DATA_DIR ?= /tmp/lcroom-parallel-$(shell id -un)
PARALLEL_DB ?= $(PARALLEL_DATA_DIR)/little-control-room.sqlite
PARALLEL_CONFIG ?= $(PARALLEL_DATA_DIR)/config.toml
PARALLEL_CRASH_LOG_DIR ?= $(PARALLEL_DATA_DIR)/crash-dumps

INCLUDE_PATHS_FLAG := $(if $(strip $(INCLUDE_PATHS)),--include-paths "$(INCLUDE_PATHS)",)
EXCLUDE_PATHS_FLAG := $(if $(strip $(EXCLUDE_PATHS)),--exclude-paths "$(EXCLUDE_PATHS)",)
ACTIVE_THRESHOLD_FLAG := $(if $(filter 20m,$(ACTIVE_THRESHOLD)),,--active-threshold "$(ACTIVE_THRESHOLD)")
STUCK_THRESHOLD_FLAG := $(if $(filter 4h,$(STUCK_THRESHOLD)),,--stuck-threshold "$(STUCK_THRESHOLD)")
INTERVAL_FLAG := $(if $(filter 60s,$(INTERVAL)),,--interval "$(INTERVAL)")
SCREENSHOT_OUTPUT_FLAG := $(if $(strip $(SCREENSHOT_OUTPUT_DIR)),--output-dir "$(SCREENSHOT_OUTPUT_DIR)",)
COMMON_FLAGS := --config "$(CONFIG)" $(INCLUDE_PATHS_FLAG) $(EXCLUDE_PATHS_FLAG) --codex-home "$(CODEX_HOME)" --opencode-home "$(OPENCODE_HOME)" --db "$(DB)" $(ACTIVE_THRESHOLD_FLAG) $(STUCK_THRESHOLD_FLAG)
PARALLEL_FLAGS := --config "$(PARALLEL_CONFIG)" $(INCLUDE_PATHS_FLAG) $(EXCLUDE_PATHS_FLAG) --codex-home "$(CODEX_HOME)" --opencode-home "$(OPENCODE_HOME)" --db "$(PARALLEL_DB)" $(ACTIVE_THRESHOLD_FLAG) $(STUCK_THRESHOLD_FLAG)

.PHONY: help tidy tidy-check fmt vet test model-eval lcagent-eval lcagent-live-eval lcagent-live-smoke lcagent-browser-smoke build build-agent build-all build-check deploy-bins install install-agent install-all clean scope scan classify doctor doctor-scan release-tools release-check release-verify release-snapshot screenshots mockups build-week-demo tui tui-parallel tui-parallel-clean serve

help:
	@echo "$(APP_NAME) Make Targets"
	@echo ""
	@echo "  make tidy            - go mod tidy"
	@echo "  make tidy-check      - verify go.mod/go.sum are tidy without changing them"
	@echo "  make fmt             - gofmt project files"
	@echo "  make vet             - run go vet ./..."
	@echo "  make test            - run go vet and go test ./..."
	@echo "  make model-eval      - run common LCR model-usage smoke checks"
	@echo "  make lcagent-eval    - run deterministic LCAgent regression evals"
	@echo "  make lcagent-live-eval - run repeatable live-provider LCAgent coding evals"
	@echo "  make lcagent-live-smoke - run a live provider LCAgent smoke test"
	@echo "  make lcagent-browser-smoke - run a scripted managed-browser LCAgent smoke test"
	@echo "  make build           - build ./$(APP)"
	@echo "  make build-agent     - build ./lcagent"
	@echo "  make build-all       - build lcroom and lcagent"
	@echo "  make build-check     - verify modules, test, and build both local binaries"
	@echo "  make deploy-bins     - rebuild repo-local ./lcroom and ./lcagent, then smoke check them"
	@echo "  make install         - go install lcroom"
	@echo "  make install-agent   - go install lcagent"
	@echo "  make install-all     - go install lcroom and lcagent"
	@echo "  make clean           - remove local build output"
	@echo "  make scope           - print effective scope for this run"
	@echo "  make scan            - one-shot scan/update"
	@echo "  make classify        - drain latest-session AI classification queue"
	@echo "  make doctor          - print cached detected artifacts/reasons"
	@echo "  make doctor-scan     - refresh state, then print detected artifacts/reasons"
	@echo "  make release-check   - run the shared local/CI release preflight"
	@echo "  make release-verify  - verify existing GoReleaser archives under dist/"
	@echo "  make release-snapshot - preflight, build, and verify local release archives"
	@echo "  make screenshots     - render curated PNG screenshots for docs"
	@echo "  make mockups         - render static high-level UI mockups"
	@echo "  make build-week-demo - run an isolated OpenAI-only recording profile"
	@echo "  make tui             - run TUI dashboard"
	@echo "  make tui-parallel    - run a second TUI using isolated config/DB under /tmp"
	@echo "  make tui-parallel-clean - remove stale /tmp TUI sandboxes not used by active runtimes"
	@echo "  make serve           - run the read-only local web/mobile client"
	@echo ""
	@echo "Config vars (override like: make scan INCLUDE_PATHS=... DB=...):"
	@echo "  DATA_DIR=$(DATA_DIR)"
	@echo "  CONFIG=$(CONFIG)"
	@echo "  INCLUDE_PATHS=$(INCLUDE_PATHS)"
	@echo "  EXCLUDE_PATHS=$(EXCLUDE_PATHS)"
	@echo "  CODEX_HOME=$(CODEX_HOME)"
	@echo "  OPENCODE_HOME=$(OPENCODE_HOME)"
	@echo "  DB=$(DB)"
	@echo "  INTERVAL=$(INTERVAL)"
	@echo "  ACTIVE_THRESHOLD=$(ACTIVE_THRESHOLD)"
	@echo "  STUCK_THRESHOLD=$(STUCK_THRESHOLD)"
	@echo "  SERVE_LISTEN=$(SERVE_LISTEN)"
	@echo "  SCREENSHOT_CONFIG=$(SCREENSHOT_CONFIG)"
	@echo "  SCREENSHOT_OUTPUT_DIR=$(SCREENSHOT_OUTPUT_DIR)"
	@echo "  MOCKUP_OUTPUT_DIR=$(MOCKUP_OUTPUT_DIR)"
	@echo "  CRASH_LOG_DIR=$(CRASH_LOG_DIR)"
	@echo "  PARALLEL_DATA_DIR=$(PARALLEL_DATA_DIR)"
	@echo "  PARALLEL_CONFIG=$(PARALLEL_CONFIG)"
	@echo "  PARALLEL_DB=$(PARALLEL_DB)"
	@echo "  PARALLEL_CRASH_LOG_DIR=$(PARALLEL_CRASH_LOG_DIR)"

tidy:
	$(GO) mod tidy

tidy-check:
	$(GO) mod tidy -diff

fmt:
	$(GO) fmt ./...
	gofmt -w ./cmd ./internal

vet:
	$(GO) vet ./...

test: vet
	$(GO) test ./...

model-eval:
	$(GO) run ./cmd/$(APP) model-eval $(COMMON_FLAGS)

lcagent-eval:
	$(GO) run ./cmd/lcagent eval

lcagent-live-eval:
	$(GO) run ./cmd/lcagent live-eval

lcagent-live-smoke:
	$(GO) run ./cmd/lcagent smoke

lcagent-browser-smoke:
	$(GO) run ./cmd/lcagent smoke --browser --data-dir "$(DATA_DIR)"

build:
	$(GO) build -o ./$(APP) ./cmd/$(APP)

build-agent:
	$(GO) build -o ./lcagent ./cmd/lcagent

build-all: build build-agent

build-check: tidy-check test build-all
	./$(APP) --version
	./lcagent --version

deploy-bins: build-all
	./$(APP) --version
	./lcagent --help >/dev/null
	@echo "deployed repo-local binaries: ./$(APP) ./lcagent"

install:
	$(GO) install ./cmd/$(APP)

install-agent:
	$(GO) install ./cmd/lcagent

install-all: install install-agent

clean:
	rm -f ./$(APP) ./lcagent

scope:
	$(GO) run ./cmd/$(APP) scope $(COMMON_FLAGS)

scan:
	$(GO) run ./cmd/$(APP) scan $(COMMON_FLAGS)

classify:
	$(GO) run ./cmd/$(APP) classify $(COMMON_FLAGS)

doctor:
	$(GO) run ./cmd/$(APP) doctor $(COMMON_FLAGS)

doctor-scan:
	$(GO) run ./cmd/$(APP) doctor $(COMMON_FLAGS) --scan

release-tools:
	@if ! command -v "$(GORELEASER)" >/dev/null 2>&1; then \
		echo "GoReleaser $(GORELEASER_VERSION) is required (macOS: brew install goreleaser)." >&2; \
		exit 1; \
	fi
	@actual_version="$$("$(GORELEASER)" --version | awk '/^GitVersion:/ { print $$2; exit }')"; \
	if [ "$$actual_version" != "$(GORELEASER_VERSION)" ]; then \
		echo "warning: local GoReleaser is $$actual_version; CI uses pinned $(GORELEASER_VERSION)" >&2; \
	fi

release-check: build-check release-tools
	$(GORELEASER) check

release-verify:
	./scripts/verify-release-snapshot.sh dist

release-snapshot: release-check
	$(GORELEASER) release --snapshot --clean --skip=notarize
	$(MAKE) release-verify

screenshots:
	$(GO) run ./cmd/$(APP) screenshots $(COMMON_FLAGS) --screenshot-config "$(SCREENSHOT_CONFIG)" $(SCREENSHOT_OUTPUT_FLAG)

mockups:
	$(GO) run ./cmd/$(APP) mockups --output-dir "$(MOCKUP_OUTPUT_DIR)"

build-week-demo:
	./scripts/run-build-week-demo.sh

tui:
	@mkdir -p "$(CRASH_LOG_DIR)"
	@log="$(CRASH_LOG_DIR)/$$(date +%Y%m%d-%H%M%S)-tui.stderr.log"; \
	$(GO) run ./cmd/$(APP) tui $(COMMON_FLAGS) $(INTERVAL_FLAG) 2> >(tee "$$log" >&2); \
	rc=$$?; \
	if [ $$rc -eq 0 ] && [ ! -s "$$log" ]; then rm -f "$$log"; fi; \
	if [ $$rc -ne 0 ]; then echo "stderr log: $$log" >&2; fi; \
	exit $$rc

tui-parallel-clean:
	@active_commands="$$(ps -axo command=)"; \
	for dir in /tmp/lcroom-parallel-*; do \
		[ -d "$$dir" ] || continue; \
		db="$$dir/little-control-room.sqlite"; \
		if printf '%s\n' "$$active_commands" | grep -F -- "--db $$db" >/dev/null; then \
			echo "Keeping active sandbox: $$dir"; \
			continue; \
		fi; \
		echo "Removing stale sandbox: $$dir"; \
		rm -rf "$$dir"; \
	done

tui-parallel:
	@$(MAKE) tui-parallel-clean
	@mkdir -p "$(PARALLEL_DATA_DIR)"
	@if [ -f "$(CONFIG)" ] && [ ! -f "$(PARALLEL_CONFIG)" ]; then cp "$(CONFIG)" "$(PARALLEL_CONFIG)"; fi
	@echo "Launching parallel TUI sandbox"
	@echo "  config: $(PARALLEL_CONFIG)"
	@echo "  db:     $(PARALLEL_DB)"
	@mkdir -p "$(PARALLEL_CRASH_LOG_DIR)"
	@log="$(PARALLEL_CRASH_LOG_DIR)/$$(date +%Y%m%d-%H%M%S)-tui.stderr.log"; \
	$(GO) run ./cmd/$(APP) tui $(PARALLEL_FLAGS) $(INTERVAL_FLAG) 2> >(tee "$$log" >&2); \
	rc=$$?; \
	if [ $$rc -eq 0 ] && [ ! -s "$$log" ]; then rm -f "$$log"; fi; \
	if [ $$rc -ne 0 ]; then echo "stderr log: $$log" >&2; fi; \
	exit $$rc

serve:
	$(GO) run ./cmd/$(APP) serve $(COMMON_FLAGS) $(INTERVAL_FLAG) --listen "$(SERVE_LISTEN)"
