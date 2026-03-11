SHELL := /bin/zsh

APP := lcroom
APP_NAME := Little Control Room
GO ?= go

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
SCREENSHOT_CONFIG ?= screenshots.local.toml
SCREENSHOT_OUTPUT_DIR ?=

INCLUDE_PATHS_FLAG := $(if $(strip $(INCLUDE_PATHS)),--include-paths "$(INCLUDE_PATHS)",)
EXCLUDE_PATHS_FLAG := $(if $(strip $(EXCLUDE_PATHS)),--exclude-paths "$(EXCLUDE_PATHS)",)
ACTIVE_THRESHOLD_FLAG := $(if $(filter 20m,$(ACTIVE_THRESHOLD)),,--active-threshold "$(ACTIVE_THRESHOLD)")
STUCK_THRESHOLD_FLAG := $(if $(filter 4h,$(STUCK_THRESHOLD)),,--stuck-threshold "$(STUCK_THRESHOLD)")
INTERVAL_FLAG := $(if $(filter 60s,$(INTERVAL)),,--interval "$(INTERVAL)")
SCREENSHOT_OUTPUT_FLAG := $(if $(strip $(SCREENSHOT_OUTPUT_DIR)),--output-dir "$(SCREENSHOT_OUTPUT_DIR)",)
COMMON_FLAGS := --config "$(CONFIG)" $(INCLUDE_PATHS_FLAG) $(EXCLUDE_PATHS_FLAG) --codex-home "$(CODEX_HOME)" --opencode-home "$(OPENCODE_HOME)" --db "$(DB)" $(ACTIVE_THRESHOLD_FLAG) $(STUCK_THRESHOLD_FLAG)

.PHONY: help tidy fmt test build install clean scope scan classify doctor doctor-scan screenshots tui serve

help:
	@echo "$(APP_NAME) Make Targets"
	@echo ""
	@echo "  make tidy            - go mod tidy"
	@echo "  make fmt             - gofmt project files"
	@echo "  make test            - run go test ./..."
	@echo "  make build           - build ./$(APP)"
	@echo "  make install         - go install the CLI"
	@echo "  make clean           - remove local build output"
	@echo "  make scope           - print effective scope for this run"
	@echo "  make scan            - one-shot scan/update"
	@echo "  make classify        - drain latest-session AI classification queue"
	@echo "  make doctor          - print cached detected artifacts/reasons"
	@echo "  make doctor-scan     - refresh state, then print detected artifacts/reasons"
	@echo "  make screenshots     - render curated PNG screenshots for docs"
	@echo "  make tui             - run TUI dashboard"
	@echo "  make serve           - run REST/WS server skeleton"
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
	@echo "  SCREENSHOT_CONFIG=$(SCREENSHOT_CONFIG)"
	@echo "  SCREENSHOT_OUTPUT_DIR=$(SCREENSHOT_OUTPUT_DIR)"

tidy:
	$(GO) mod tidy

fmt:
	$(GO) fmt ./...
	gofmt -w ./cmd ./internal

test:
	$(GO) test ./...

build:
	$(GO) build -o ./$(APP) ./cmd/$(APP)

install:
	$(GO) install ./cmd/$(APP)

clean:
	rm -f ./$(APP)

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

screenshots:
	$(GO) run ./cmd/$(APP) screenshots $(COMMON_FLAGS) --screenshot-config "$(SCREENSHOT_CONFIG)" $(SCREENSHOT_OUTPUT_FLAG)

tui:
	$(GO) run ./cmd/$(APP) tui $(COMMON_FLAGS) $(INTERVAL_FLAG)

serve:
	$(GO) run ./cmd/$(APP) serve $(COMMON_FLAGS) $(INTERVAL_FLAG)
