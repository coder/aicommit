.ONESHELL:
SHELL := /bin/bash

.PHONY: build
# Build the aicommit binary
build:
	set -e
	mkdir -p bin
	VERSION=$$(git describe --tags --always --dirty || echo "dev")
	echo "Building version $${VERSION}"
	go build -ldflags "-X main.Version=$${VERSION}" -o bin/aicommit ./cmd/aicommit
