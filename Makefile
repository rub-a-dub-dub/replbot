GO=$(shell which go)
VERSION := $(shell git describe --tags --always 2>/dev/null || echo "dev")

.PHONY:

help:
	@echo "Typical commands:"
	@echo "  make check                       - Run all tests, vetting/formatting checks and linters"
	@echo "  make fmt build-snapshot install  - Build latest and install to local system"
	@echo
	@echo "Test/check:"
	@echo "  make test                        - Run tests"
	@echo "  make race                        - Run tests with -race flag"
	@echo "  make coverage                    - Run tests and show coverage"
	@echo "  make coverage-html               - Run tests and show coverage (as HTML)"
	@echo "  make coverage-upload             - Upload coverage results to codecov.io"
	@echo
	@echo "Lint/format:"
	@echo "  make fmt                         - Run 'go fmt'"
	@echo "  make fmt-check                   - Run 'go fmt', but don't change anything"
	@echo "  make vet                         - Run 'go vet'"
	@echo "  make lint                        - Run 'golangci-lint'"
	@echo "  make staticcheck                 - Run 'staticcheck'"
	@echo
	@echo "Build:"
	@echo "  make build                       - Build"
	@echo "  make build-snapshot              - Build snapshot"
	@echo "  make build-simple                - Build (using go build, without goreleaser)"
	@echo "  make clean                       - Clean build folder"
	@echo
	@echo "Releasing (requires goreleaser):"
	@echo "  make release                     - Create a release"
	@echo "  make release-snapshot            - Create a test release"
	@echo
	@echo "Install locally (requires sudo):"
	@echo "  make install                     - Copy binary from dist/ to /usr/bin"
	@echo "  make install-deb                 - Install .deb from dist/"


# Test/check targets

check: test fmt-check vet lint staticcheck

test: .PHONY
	$(GO) test ./...

race: .PHONY
	$(GO) test -race ./...

coverage:
	mkdir -p build/coverage
	$(GO) test -race -coverprofile=build/coverage/coverage.txt -covermode=atomic ./...
	$(GO) tool cover -func build/coverage/coverage.txt

coverage-html:
	mkdir -p build/coverage
	$(GO) test -race -coverprofile=build/coverage/coverage.txt -covermode=atomic ./...
	$(GO) tool cover -html build/coverage/coverage.txt

coverage-upload:
	cd build/coverage && (curl -s https://codecov.io/bash | bash)

# Lint/formatting targets

fmt:
	$(GO) fmt ./...

fmt-check:
	test -z $(shell gofmt -l .)

vet:
	$(GO) vet ./...

lint:
	which golangci-lint || $(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	golangci-lint run

staticcheck: .PHONY
	rm -rf build/staticcheck
	which staticcheck || $(GO) install honnef.co/go/tools/cmd/staticcheck@latest
	mkdir -p build/staticcheck
	ln -s "$(GO)" build/staticcheck/go
	PATH="$(PWD)/build/staticcheck:$(PATH)" staticcheck ./...
	rm -rf build/staticcheck

# Building targets

build: .PHONY
	goreleaser build --rm-dist

build-snapshot:
	goreleaser build --snapshot --rm-dist

build-simple: clean
	mkdir -p dist/replbot_linux_amd64
	$(GO) build \
		-o dist/replbot_linux_amd64/replbot \
		-ldflags \
		"-s -w -X main.version=$(VERSION) -X main.commit=$(shell git rev-parse --short HEAD) -X main.date=$(shell date +%s)"

clean: .PHONY
	rm -rf dist build


# Releasing targets

release:
	goreleaser release --rm-dist

release-snapshot:
	goreleaser release --snapshot --skip-publish --rm-dist


# Installing targets

install:
	sudo rm -f /usr/bin/replbot
	sudo cp -a dist/replbot_linux_amd64/replbot /usr/bin/replbot

install-deb:
	sudo systemctl stop replbot || true
	sudo apt-get purge replbot || true
	sudo dpkg -i dist/*.deb
