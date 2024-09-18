.ONESHELL:
SHELL := /bin/bash

.PHONY: build
# Build the aicommit binary
build:
	set -e
	mkdir -p bin
	# version may be passed in via homebrew formula
	if [ -z "$$VERSION" ]; then \
		VERSION=$$(git describe --tags --always --dirty || echo "dev"); \
	fi
	echo "Building version $${VERSION}"
	go build -ldflags "-X main.Version=$${VERSION}" -o bin/aicommit ./cmd/aicommit
