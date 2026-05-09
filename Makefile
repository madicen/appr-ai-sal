BINARY := appr-ai-sal
PKG    := ./cmd/appr-ai-sal
PREFIX ?= $(HOME)/.local

.PHONY: build install run tidy fmt vet test clean

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

clean:
	rm -f $(BINARY)
	rm -rf dist bin
