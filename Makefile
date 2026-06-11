.PHONY: fmt lint build test check setup-hooks

VERSION ?= v1.0.22
LDFLAGS ?= -s -w -X github.com/pinealctx/kiro-gateway/version.Version=$(VERSION)

fmt:
	go fmt ./...

lint:
	golangci-lint run

build:
	go build -ldflags "$(LDFLAGS)" ./...

test:
	go test ./...

check: fmt lint build test

setup-hooks:
	@cp scripts/pre-commit .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit 2>/dev/null || true
	@echo "pre-commit hook installed"
