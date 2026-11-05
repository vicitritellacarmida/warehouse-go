# Common tasks. Integration tests need a MySQL server; set TEST_DATABASE_DSN
# to a DSN whose database name ends with _test.

GO ?= go
BINARY ?= bin/warehouse
TEST_DATABASE_DSN ?= root:@tcp(127.0.0.1:3306)/warehouse_test

.PHONY: run migrate build test test-integration lint cover

run:
	$(GO) run ./cmd/warehouse serve

migrate:
	$(GO) run ./cmd/warehouse migrate

build:
	$(GO) build -o $(BINARY) ./cmd/warehouse

# Unit tests only. Integration tests skip because TEST_DATABASE_DSN is unset.
test:
	env -u TEST_DATABASE_DSN $(GO) test ./...

# Unit and integration tests against MySQL, with the race detector.
test-integration:
	TEST_DATABASE_DSN='$(TEST_DATABASE_DSN)' $(GO) test -race -count=1 ./...

lint:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...

cover:
	TEST_DATABASE_DSN='$(TEST_DATABASE_DSN)' $(GO) test -count=1 -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1
