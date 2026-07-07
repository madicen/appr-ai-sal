BINARY := appr-ai-sal
PKG    := ./cmd/appr-ai-sal
PREFIX ?= $(HOME)/.local

# Demo / VHS plumbing. APPR_AI_SAL_DEMO_DIR is the per-recording sandbox
# that the demo binary uses for config + cache; the fixture script
# (scripts/setup-demo-fixtures.sh) pre-seeds it with a mix of "fresh"
# and "missing" agent briefs so the GIFs render the same shape every
# time.
DEMO_DIR    := $(CURDIR)/tmp/demo
TAPES       := $(wildcard vhs/*.tape)
GIF_TARGETS := $(patsubst vhs/%.tape,gif-%,$(TAPES))

# gif-* targets are deliberately *not* in .PHONY so the gif-% pattern
# rule below applies (PHONY targets skip implicit/pattern rule
# matching). They run unconditionally because the recipe doesn't
# produce a file matching the target name — vhs writes to whatever
# Output the tape names, not to a "gif-foo" file.
.PHONY: build install run tidy fmt vet test test-race cover lint clean demo \
        vhs-fixtures screenshots clean-screenshots

build:
	go build -o $(BINARY) $(PKG)

install:
	go install $(PKG)

run:
	go run $(PKG)

tidy:
	go mod tidy

fmt:
	gofmt -s -w .

vet:
	go vet ./...

test:
	go test ./...

# test-race — run the suite under the race detector (mirrors CI).
test-race:
	go test -race ./...

# cover — run the suite with coverage reporting (mirrors CI).
cover:
	go test -cover ./...

# lint — run golangci-lint over the module. Install it with:
#   go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
# (or `brew install golangci-lint`). CI runs the same linter via
# golangci/golangci-lint-action; see .github/workflows/ci.yml.
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
	  echo "error: golangci-lint not found on PATH."; \
	  echo "install it with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	  exit 1; \
	}
	golangci-lint run ./...

clean:
	rm -f $(BINARY)
	rm -rf dist bin

# demo — build and launch the demo binary against a freshly-seeded
# fixture dir. Useful for hand-testing tape timings interactively
# before running them through vhs.
demo: build vhs-fixtures
	APPR_AI_SAL_DEMO_DIR=$(DEMO_DIR) ./$(BINARY) --demo

# vhs-fixtures — re-seed $(DEMO_DIR) with the canned repo-agents /
# lang-agents / tech-agents fixtures. Idempotent; safe to run before
# every screenshot pass.
vhs-fixtures:
	APPR_AI_SAL_DEMO_DIR=$(DEMO_DIR) bash scripts/setup-demo-fixtures.sh

# screenshots — regenerate every GIF under screenshots/ from the tapes
# in vhs/. Each tape exports its own filename via "Output screenshots/...".
# Requires: brew install vhs (https://github.com/charmbracelet/vhs).
screenshots: build vhs-fixtures $(GIF_TARGETS)

# Per-tape regen target. `make gif-review-run` re-records just the
# hero tape, which is what you want during tape-timing iteration.
gif-%: vhs/%.tape build vhs-fixtures
	@command -v vhs >/dev/null 2>&1 || { \
	  echo "error: vhs not found on PATH; install with 'brew install vhs'"; \
	  exit 1; \
	}
	mkdir -p screenshots
	APPR_AI_SAL_DEMO_DIR=$(DEMO_DIR) vhs $<

clean-screenshots:
	rm -rf screenshots tmp/demo
