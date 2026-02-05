.PHONY: build install clean test docker-build help

# Binary name
BINARY_NAME=mig

# Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	go build -o $(BINARY_NAME) ./cmd/main.go

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

# Build docker image
docker-build:
	@echo "Building Docker image..."
	docker build -t mystaline/migration-tool:latest .

# Show help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  build         Build the $(BINARY_NAME) binary"
	@echo "  install       Build and install $(BINARY_NAME) to /usr/local/bin (requires sudo)"
	@echo "  clean         Remove build artifacts"
	@echo "  test          Run go tests (not migration tests)"
	@echo "  docker-build  Build the Docker image"
	@echo "  help          Show this help message"
