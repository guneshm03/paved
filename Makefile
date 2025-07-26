# paved — one gate for everything: `make check`.
GO        ?= go
GOLANGCI_LINT ?= golangci-lint
COVER_MIN ?= 85
GOFLAGS   ?= -p=3
export GOFLAGS

GOFILES = $(shell find cmd internal -name '*.go' -not -path '*/testdata/*')

.PHONY: check fmt fmt-check vet lint test cover build demo bench fuzz clean

## check: format-check + vet + lint + tests (race) + coverage threshold
check: fmt-check vet lint test cover

fmt:
	gofmt -w $(GOFILES)

fmt-check:
	@out="$$(gofmt -l $(GOFILES))"; if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi
	@echo "fmt-check: ok"

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

test:
	$(GO) test -race -count=1 -covermode=atomic -coverprofile=coverage.out ./...

cover: test
	./scripts/coverage.sh coverage.out $(COVER_MIN)

build:
	$(GO) build -o bin/paved ./cmd/paved

demo: build
	./scripts/demo.sh

bench:
	$(GO) test -run '^$$' -bench . -benchmem ./internal/catalog ./internal/textdiff

fuzz:
	$(GO) test -run '^$$' -fuzz FuzzParseRef -fuzztime 20s ./internal/catalog
	$(GO) test -run '^$$' -fuzz FuzzParseDescriptors -fuzztime 20s ./internal/catalog

clean:
	rm -rf bin coverage.out .demo-out
