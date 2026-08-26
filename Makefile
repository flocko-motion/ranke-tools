# ranke-tools — repo-level targets.
#
# One Go module, one subdirectory per tool (see README.md). No generation step here —
# unlike ranke-db, nothing in this repo is derived from a spec.

RANKE_GRAPH_REPO ?= https://github.com/flocko-motion/ranke-graph
RANKE_GRAPH_REF  ?= main
PAPERS_DIR       := docs/papers
# Everything at the top of the paper repo is reference material and gets pulled;
# these are the exceptions (its own tooling). Dotdirs never match the glob.
PAPERS_SKIP      := scripts

# brokkr, installed on demand rather than assumed present. Cached under bin/ (already
# gitignored, already this repo's build-output directory) — the installer itself checks
# the latest release against what is already there and only downloads on a mismatch.
TOOLS_BIN         := bin/tools
BROKKR            := $(TOOLS_BIN)/brokkr
BROKKR_INSTALL_SH := https://raw.githubusercontent.com/flocko-motion/sindri/master/scripts/install-brokkr.sh

# One binary per top-level tool directory — add a name here when a new tool joins
# gitbackup, rather than hand-writing a second build recipe for it.
TOOLS := gitbackup

.PHONY: all help build test vet fmt lint check tidy docs docs-clean

.DEFAULT_GOAL := all

all: check ## Default: the whole quality gate

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## Build every tool's binary into bin/
	@for t in $(TOOLS); do \
		echo ">> build   → bin/$$t"; \
		go build -o bin/$$t ./$$t || exit 1; \
	done

test: ## Test all packages; scope with make test/<pkg> (e.g. test/gitbackup)
	@echo ">> go test ./..."
	@go test ./...

test/%:
	@echo ">> go test -v ./$*/..."
	@go test -v ./$*/...

vet: ## go vet every package
	@go vet ./...

fmt: ## Check gofmt cleanliness (does not rewrite — see: gofmt -w .)
	@fmt=$$(gofmt -l $$(git ls-files '*.go')); \
	[ -z "$$fmt" ] || { echo "gofmt needed:"; echo "$$fmt"; exit 1; }

lint: ## Run brokkr lint — one already on PATH if there is one, else this repo's cached copy in bin/tools/ (the installer checks GitHub for a newer release every run, skipping the download itself when already current)
	@if command -v brokkr >/dev/null 2>&1; then bin=brokkr; \
	else \
		command -v curl >/dev/null 2>&1 || { echo "ERROR: brokkr not found and curl is not on PATH to install it"; exit 1; }; \
		curl -fsSL $(BROKKR_INSTALL_SH) | bash -s -- $(BROKKR); \
		bin=$(BROKKR); \
	fi; \
	"$$bin" lint

check: ## Whole-repo quality gate: build, vet, gofmt-check, test, lint
	@set -e; \
		$(MAKE) --no-print-directory build; \
		$(MAKE) --no-print-directory vet; \
		$(MAKE) --no-print-directory fmt; \
		$(MAKE) --no-print-directory test; \
		$(MAKE) --no-print-directory lint

tidy: ## go mod tidy
	@go mod tidy

docs: ## Pull the latest ranke-graph documents (papers, spec, glossary) into docs/papers/
	@echo ">> fetching ranke-graph documents into $(PAPERS_DIR)/"
	@tmp=$$(mktemp -d) && \
		git clone --depth 1 --branch $(RANKE_GRAPH_REF) $(RANKE_GRAPH_REPO) $$tmp >/dev/null 2>&1 && \
		rm -rf $(PAPERS_DIR) && mkdir -p $(PAPERS_DIR) && \
		for d in $$tmp/*/; do \
			name=$$(basename $$d); \
			case " $(PAPERS_SKIP) " in *" $$name "*) continue ;; esac; \
			cp -r $$d $(PAPERS_DIR)/; \
		done && \
		cp $$tmp/LICENSE $(PAPERS_DIR)/LICENSE 2>/dev/null || true; \
		rm -rf $$tmp; \
		echo ">> pulled $$(find $(PAPERS_DIR) -name '*.typ' | wc -l | tr -d ' ') document(s):"; \
		find $(PAPERS_DIR) -name '*.typ' | sort | sed 's|^|     |'

docs-clean: ## Remove the pulled paper references
	rm -rf $(PAPERS_DIR)
