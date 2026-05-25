.PHONY: build install clean test docker-build tag help

BINARY_NAME=mig
REGISTRY=ghcr.io
GHCR_OWNER?=mystaline
IMAGE=$(REGISTRY)/$(GHCR_OWNER)/migration-tool

# Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	CGO_ENABLED=0 go build -o $(BINARY_NAME) ./cmd/main.go

# Install the binary system-wide
install: build
	@echo "Installing $(BINARY_NAME) to /usr/local/bin..."
	sudo mv $(BINARY_NAME) /usr/local/bin/

# Remove build artifacts
clean:
	@echo "Cleaning up..."
	rm -f $(BINARY_NAME)
	go clean

# Run go tests (not migration tests)
test:
	@echo "Running go tests..."
	go test -v ./...

# Build docker image locally
docker-build:
	@echo "Building Docker image..."
	docker build -t $(IMAGE):latest .

# Git tag for release (CI auto-publishes Docker image + binaries)
# Usage: make tag v=1.2.3
tag:
	@if [ -z "$(v)" ]; then echo "Usage: make tag v=1.2.3"; exit 1; fi; \
	if git rev-parse "v$(v)" >/dev/null 2>&1; then echo "tag v$(v) already exists"; exit 1; fi; \
	git tag -a "v$(v)" -m "v$(v)" && \
	echo "Tagged v$(v)" && \
	echo "Run 'git push --tags' to trigger CI release"

# Show help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  build         Build the $(BINARY_NAME) binary"
	@echo "  install       Build and install $(BINARY_NAME) to /usr/local/bin (requires sudo)"
	@echo "  clean         Remove build artifacts"
	@echo "  test          Run go tests (not migration tests)"
	@echo "  docker-build  Build the Docker image locally"
	@echo "  tag           Create a git version tag (make tag v=1.2.3)"
	@echo "  help          Show this help message"
