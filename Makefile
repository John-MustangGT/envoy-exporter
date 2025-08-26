# Enhanced Makefile for Envoy Prometheus Exporter
# Builds with comprehensive version and build information

# Binary name
BINARY_NAME=envoy-prometheus-exporter

# Get build information
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
BUILD_USER := $(shell whoami)
BUILD_HOST := $(shell hostname)
GO_VERSION := $(shell go version | sed 's/go version //')
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_BRANCH := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
GIT_TAG := $(shell git describe --tags --exact-match 2>/dev/null || echo "")
GIT_DIRTY := $(shell git diff --quiet 2>/dev/null || echo "-dirty")
VERSION := $(if $(GIT_TAG),$(GIT_TAG),v0.0.0-$(GIT_COMMIT)$(GIT_DIRTY))

# Platform information
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)
PLATFORM := $(GOOS)/$(GOARCH)

# Go module information
MODULE_NAME := $(shell go list -m 2>/dev/null || echo "envoy-prometheus-exporter")
GO_MOD_VERSION := $(shell go list -m -f '{{.GoVersion}}' 2>/dev/null || echo "unknown")

# Build flags with version information
LDFLAGS := -s -w \
	-X 'main.Version=$(VERSION)' \
	-X 'main.GitCommit=$(GIT_COMMIT)' \
	-X 'main.GitBranch=$(GIT_BRANCH)' \
	-X 'main.BuildTime=$(BUILD_TIME)' \
	-X 'main.BuildUser=$(BUILD_USER)' \
	-X 'main.BuildHost=$(BUILD_HOST)' \
	-X 'main.GoVersion=$(GO_VERSION)' \
	-X 'main.Platform=$(PLATFORM)' \
	-X 'main.ModuleName=$(MODULE_NAME)' \
	-X 'main.GoModVersion=$(GO_MOD_VERSION)'

# Default target
.PHONY: all
all: clean build

# Build the binary
.PHONY: build
build: deps
	@echo "Building $(BINARY_NAME) $(VERSION) for $(PLATFORM)..."
	@echo "Git: $(GIT_COMMIT) on $(GIT_BRANCH)"
	@echo "Go: $(GO_VERSION)"
	@echo "Time: $(BUILD_TIME)"
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) .

# Build for multiple platforms
.PHONY: build-all
build-all: clean deps
	@echo "Building for multiple platforms..."
	@mkdir -p dist
	# Linux AMD64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)-linux-amd64 .
	# Linux ARM64
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)-linux-arm64 .
	# Linux ARM (Raspberry Pi)
	GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)-linux-arm .
	# Windows AMD64
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)-windows-amd64.exe .
	# macOS AMD64
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)-darwin-amd64 .
	# macOS ARM64 (Apple Silicon)
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)-darwin-arm64 .
	@echo "Built binaries:"
	@ls -la dist/

# Development build with debug info
.PHONY: build-dev
build-dev: deps
	@echo "Building development version..."
	go build -race -ldflags "$(LDFLAGS)" -o $(BINARY_NAME)-dev .

# Install dependencies
.PHONY: deps
deps:
	@echo "Installing dependencies..."
	go mod download
	go mod tidy

# Run tests
.PHONY: test
test:
	@echo "Running tests..."
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Run linter
.PHONY: lint
lint:
	@echo "Running linter..."
	golangci-lint run

# Format code
.PHONY: fmt
fmt:
	@echo "Formatting code..."
	go fmt ./...
	goimports -w .

# Generate version info for web files
.PHONY: web-version
web-version:
	@echo "Generating web version info..."
	@mkdir -p web
	@echo '{"version":"$(VERSION)","git_commit":"$(GIT_COMMIT)","git_branch":"$(GIT_BRANCH)","build_time":"$(BUILD_TIME)","build_user":"$(BUILD_USER)","build_host":"$(BUILD_HOST)","go_version":"$(GO_VERSION)","platform":"$(PLATFORM)","module_name":"$(MODULE_NAME)"}' > web/version.json

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	rm -f $(BINARY_NAME) $(BINARY_NAME)-dev
	rm -rf dist/
	rm -f coverage.out coverage.html
	rm -f web/version.json

# Install the binary
.PHONY: install
install: build
	@echo "Installing $(BINARY_NAME) to /usr/local/bin/..."
	sudo cp $(BINARY_NAME) /usr/local/bin/

# Create a release package
.PHONY: package
package: build-all web-version
	@echo "Creating release package..."
	@mkdir -p release
	# Create source package
	git archive --format=tar.gz --prefix=$(BINARY_NAME)-$(VERSION)/ HEAD > release/$(BINARY_NAME)-$(VERSION)-source.tar.gz
	# Package binaries
	cd dist && for binary in *; do \
		tar -czf ../release/$$binary-$(VERSION).tar.gz $$binary; \
	done
	@echo "Release packages created in release/ directory"

# Docker build
.PHONY: docker
docker:
	@echo "Building Docker image..."
	docker build -t $(BINARY_NAME):$(VERSION) \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		.

# Show build information
.PHONY: info
info:
	@echo "Build Information:"
	@echo "  Version:      $(VERSION)"
	@echo "  Git Commit:   $(GIT_COMMIT)"
	@echo "  Git Branch:   $(GIT_BRANCH)"
	@echo "  Build Time:   $(BUILD_TIME)"
	@echo "  Build User:   $(BUILD_USER)"
	@echo "  Build Host:   $(BUILD_HOST)"
	@echo "  Go Version:   $(GO_VERSION)"
	@echo "  Platform:     $(PLATFORM)"
	@echo "  Module:       $(MODULE_NAME)"
	@echo "  Go Mod Ver:   $(GO_MOD_VERSION)"

# Run the application with sample config
.PHONY: run
run: build
	./$(BINARY_NAME) envoy_config.xml

# Development server with auto-restart
.PHONY: dev
dev:
	@echo "Starting development server with auto-restart..."
	@command -v air >/dev/null 2>&1 || { echo "Installing air for auto-restart..."; go install github.com/cosmtrek/air@latest; }
	air

# Generate systemd service file
.PHONY: systemd
systemd:
	@echo "Generating systemd service file..."
	@echo '[Unit]' > $(BINARY_NAME).service
	@echo 'Description=Envoy Prometheus Exporter' >> $(BINARY_NAME).service
	@echo 'After=network.target' >> $(BINARY_NAME).service
	@echo '' >> $(BINARY_NAME).service
	@echo '[Service]' >> $(BINARY_NAME).service
	@echo 'Type=simple' >> $(BINARY_NAME).service
	@echo 'User=envoy-exporter' >> $(BINARY_NAME).service
	@echo 'Group=envoy-exporter' >> $(BINARY_NAME).service
	@echo 'WorkingDirectory=/opt/envoy-exporter' >> $(BINARY_NAME).service
	@echo 'ExecStart=/usr/local/bin/$(BINARY_NAME) /opt/envoy-exporter/envoy_config.xml' >> $(BINARY_NAME).service
	@echo 'Restart=always' >> $(BINARY_NAME).service
	@echo 'RestartSec=10' >> $(BINARY_NAME).service
	@echo 'StandardOutput=journal' >> $(BINARY_NAME).service
	@echo 'StandardError=journal' >> $(BINARY_NAME).service
	@echo 'SyslogIdentifier=envoy-exporter' >> $(BINARY_NAME).service
	@echo '' >> $(BINARY_NAME).service
	@echo '[Install]' >> $(BINARY_NAME).service
	@echo 'WantedBy=multi-user.target' >> $(BINARY_NAME).service
	@echo "Systemd service file created: $(BINARY_NAME).service"
	@echo "To install: sudo cp $(BINARY_NAME).service /etc/systemd/system/"

# Help
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build       - Build the binary for current platform"
	@echo "  build-all   - Build for multiple platforms"
	@echo "  build-dev   - Build development version with debug info"
	@echo "  clean       - Clean build artifacts"
	@echo "  deps        - Install dependencies"
	@echo "  test        - Run tests with coverage"
	@echo "  lint        - Run linter"
	@echo "  fmt         - Format code"
	@echo "  install     - Install binary to /usr/local/bin"
	@echo "  package     - Create release packages"
	@echo "  docker      - Build Docker image"
	@echo "  systemd     - Generate systemd service file"
	@echo "  web-version - Generate web version info"
	@echo "  info        - Show build information"
	@echo "  run         - Build and run with sample config"
	@echo "  dev         - Start development server with auto-restart"
	@echo "  help        - Show this help"

