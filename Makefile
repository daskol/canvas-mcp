.PHONY: build test check

build:
	go build -o bin/canvas-mcp ./cmd/canvas-mcp

test:
	go test ./...

check:
	go test -race ./...
	go vet ./...
