# planprice build gates. `make check` is what CI runs.
# Tools are taken from PATH, falling back to user-local installs.
export PATH := $(HOME)/.local/go/bin:$(HOME)/.local/bin:$(PATH)
export GOFLAGS ?= -p=3

GO            ?= $(firstword $(shell command -v go 2>/dev/null) $(wildcard $(HOME)/.local/go/bin/go) go)
GOLANGCI_LINT ?= $(firstword $(shell command -v golangci-lint 2>/dev/null) $(wildcard $(HOME)/.local/bin/golangci-lint) golangci-lint)
COVER_MIN     ?= 80
BIN           := bin/planprice

.PHONY: all build check fmt fmt-check lint vet test cover demo bench snapshot plans clean

all: build

build:
	$(GO) build -o $(BIN) ./cmd/planprice

## check: every gate CI runs (format, lint, vet, tests, coverage threshold)
check: fmt-check lint vet test cover

fmt:
	gofmt -w .

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

lint:
	$(GOLANGCI_LINT) run ./...

vet:
	$(GO) vet ./...

test:
	$(GO) test -race -count=1 ./...

## cover: coverage of internal/ (the core logic) must be >= COVER_MIN percent
cover:
	$(GO) test -count=1 -coverpkg=./internal/... -coverprofile=coverage.out ./... >/dev/null
	@$(GO) tool cover -func=coverage.out | awk -v min=$(COVER_MIN) '/^total:/ { sub("%","",$$3); printf "internal/ coverage: %.1f%% (minimum %d%%)\n", $$3, min; if ($$3+0 < min) exit 1 }'

## demo: offline end-to-end run against the bundled plans and usage file
demo: build
	./scripts/demo.sh

bench:
	$(GO) test -run '^$$' -bench . -benchmem ./internal/...

## snapshot: refresh the embedded AWS price snapshot (network, ~1 GB streamed)
snapshot:
	$(GO) run ./cmd/pricegen fetch --cache .cache/offers
	$(GO) run ./cmd/pricegen build --cache .cache/offers --out internal/pricedata/aws-prices.json

## plans: regenerate testdata/plans from examples/ (terraform + registry access)
plans:
	./scripts/gen-plans.sh

clean:
	rm -rf bin coverage.out
