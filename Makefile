.PHONY: build test lint clean fmt check

build:
	go build ./...

test:
	go test ./... -count=1 -timeout 60s

test-race:
	go test ./... -race -count=1 -timeout 120s

lint:
	golangci-lint run ./...

clean:
	rm -f nanobot.exe

fmt:
	go fmt ./...

check: fmt lint test
