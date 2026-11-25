# Genisys — build orchestration (docs/IMPLEMENTATION.md P0-1)
GO      ?= go
BIN     := genisys
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
DISTOS  := linux/amd64 linux/arm64 darwin/arm64 windows/amd64

.PHONY: all build test testrace lint fmt vet ui embed-check deps-audit clean

all: build

e2e: ## browser e2e against serve + mock endpoint (needs node + chromium)
	CGO_ENABLED=0 go build -o dist/genisys ./cmd/genisys
	node e2e/run.mjs

build:
	CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/genisys

test:
	$(GO) test ./...

testrace:
	CGO_ENABLED=0 $(GO) test -race -count=1 ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

lint: vet
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed — running go vet only"

# Frontend (P3b). No-op until ui/ exists.
ui:
	@if [ -f ui/package.json ]; then cd ui && npm ci && npm run build; else echo "ui/ not present yet — skipping"; fi

embed-check:
	@test -d ui/dist && echo "ui/dist present" || echo "ui/dist absent (ok until P3)"

dist-tar:
	@mkdir -p dist
	@for t in $(DISTOS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=""; [ "$$os" = windows ] && ext=.exe; \
		echo "building $$t"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -ldflags "$(LDFLAGS)" -o dist/$(BIN)-$$os-$$arch$$ext ./cmd/genisys || exit 1; \
	done
	@cd dist && for f in genisys-*; do \
	  case "$$f" in *.tar.gz|checksums.txt) continue;; esac; \
	  tar czf "$${f%.exe}.tar.gz" "$$f" && rm -f "$$f"; \
	done
	@cd dist && (sha256sum * 2>/dev/null || shasum -a 256 *) > checksums.txt

dist-appimage:
	@mkdir -p dist
	VERSION=$(VERSION) COMMIT=$(COMMIT) ./scripts/appimage.sh

# release flow (SPEC §19.1): tag → make dist → GitHub Release
dist: dist-tar dist-appimage

.PHONY: dist dist-tar dist-appimage

deps-audit:
	@n=$$(grep -c '^\t' <(awk '/^require \(/{f=1;next}/^\)/{f=0}f&&!/\/\/ indirect/' go.mod) 2>/dev/null || echo 0); \
	echo "direct dependencies: $$n"; [ "$$n" -le 25 ] || { echo "FAIL: >25 direct deps (IMPL §3)"; exit 1; }

clean:
	rm -f $(BIN); rm -rf dist
