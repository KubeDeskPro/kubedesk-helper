.PHONY: all build build-amd64 build-arm64 build-universal clean install test release

BINARY_NAME=kubedesk-helper
VERSION=2.0.0
BUILD_DIR=build
INSTALL_DIR=/usr/local/bin
RELEASE_DIR=$(BUILD_DIR)/release

all: build-universal

# Build for current architecture
build:
	@echo "Building for current architecture..."
	go build -o $(BUILD_DIR)/$(BINARY_NAME) .

# Build for amd64
build-amd64:
	@echo "Building for amd64..."
	GOARCH=amd64 GOOS=darwin go build -o $(BUILD_DIR)/$(BINARY_NAME)-amd64 .

# Build for arm64
build-arm64:
	@echo "Building for arm64..."
	GOARCH=arm64 GOOS=darwin go build -o $(BUILD_DIR)/$(BINARY_NAME)-arm64 .

# Build universal binary (amd64 + arm64)
build-universal: build-amd64 build-arm64
	@echo "Creating universal binary..."
	@mkdir -p $(BUILD_DIR)
	lipo -create -output $(BUILD_DIR)/$(BINARY_NAME) \
		$(BUILD_DIR)/$(BINARY_NAME)-amd64 \
		$(BUILD_DIR)/$(BINARY_NAME)-arm64
	@echo "Universal binary created at $(BUILD_DIR)/$(BINARY_NAME)"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)

# Install to /usr/local/bin
install: build-universal
	@echo "Installing $(BINARY_NAME) to $(INSTALL_DIR)..."
	sudo cp $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME)
	sudo chmod +x $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "Installed successfully!"

# Run tests
test:
	@echo "Running tests..."
	go test -v ./...

# Run the helper
run: build
	@echo "Starting $(BINARY_NAME)..."
	$(BUILD_DIR)/$(BINARY_NAME)

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...

# Lint code
lint:
	@echo "Linting code..."
	golangci-lint run

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

# Show version
version:
	@echo "$(BINARY_NAME) version $(VERSION)"

# Create release tarball
# Usage: make release VERSION=x.y.z
release:
ifndef VERSION
	$(error VERSION is required. Usage: make release VERSION=x.y.z)
endif
	@echo "Creating release $(VERSION)..."
	@mkdir -p $(RELEASE_DIR)
	@echo "Building ARM64 binary for version $(VERSION)..."
	@GOARCH=arm64 GOOS=darwin go build -ldflags "-X main.version=$(VERSION)" -o $(BUILD_DIR)/$(BINARY_NAME)-arm64 .
	@echo "Building AMD64 binary for version $(VERSION)..."
	@GOARCH=amd64 GOOS=darwin go build -ldflags "-X main.version=$(VERSION)" -o $(BUILD_DIR)/$(BINARY_NAME)-amd64 .
	@echo "Creating universal binary..."
	@lipo -create -output $(BUILD_DIR)/$(BINARY_NAME) \
		$(BUILD_DIR)/$(BINARY_NAME)-arm64 \
		$(BUILD_DIR)/$(BINARY_NAME)-amd64
	@echo "Creating tarball..."
	@tar -czf $(RELEASE_DIR)/$(BINARY_NAME)-$(VERSION).tar.gz -C $(BUILD_DIR) $(BINARY_NAME)
	@echo "Release tarball created: $(RELEASE_DIR)/$(BINARY_NAME)-$(VERSION).tar.gz"
	@shasum -a 256 $(RELEASE_DIR)/$(BINARY_NAME)-$(VERSION).tar.gz

