.PHONY: build build-cuda test lint clean run help package install check-requirements install-systemd-system install-systemd-user uninstall-systemd-system uninstall-systemd-user

BINARY_NAME=virtualcam
BIN_DIR=bin
CMD_DIR=cmd/virtualcam
MAIN_FILE=$(CMD_DIR)/main.go
VERSION ?= 1.0.0

help:
	@echo "Virtual Cameraman - Makefile targets"
	@echo "  make check-requirements - Check if all dependencies are installed"
	@echo "  make build       - Build the binary (no CUDA)"
	@echo "  make build-cuda  - Build with CUDA support (-tags cuda)"
	@echo "  make run         - Build and run"
	@echo "  make test        - Run tests"
	@echo "  make clean       - Clean build artifacts"
	@echo "  make lint        - Check formatting and run go vet"
	@echo "  make package     - Create distribution package (tar.gz)"
	@echo "  make install     - Install binary to /usr/local/bin"
	@echo "  make install-systemd-system - Install system service unit"
	@echo "  make uninstall-systemd-system - Remove system service unit"
	@echo "  make install-systemd-user   - Install user service unit"
	@echo "  make uninstall-systemd-user - Remove user service unit"
	@echo "  make help        - Show this help message"

check-requirements:
	@./check-requirements.sh

build:
	@mkdir -p $(BIN_DIR)
	@echo "Building $(BINARY_NAME)..."
	go build -o $(BIN_DIR)/$(BINARY_NAME) $(MAIN_FILE)
	@echo "✓ Built: $(BIN_DIR)/$(BINARY_NAME)"

build-cuda:
	@mkdir -p $(BIN_DIR)
	@echo "Building $(BINARY_NAME) with CUDA support..."
	go build -tags cuda -o $(BIN_DIR)/$(BINARY_NAME) $(MAIN_FILE)
	@echo "✓ Built: $(BIN_DIR)/$(BINARY_NAME)"

run: build
	@echo "Running $(BINARY_NAME)..."
	./$(BIN_DIR)/$(BINARY_NAME)

test:
	@echo "Running tests..."
	go test -v ./...

clean:
	@echo "Cleaning..."
	rm -rf $(BIN_DIR) build
	go clean
	@echo "✓ Clean complete"

lint:
	@echo "Checking formatting..."
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal && exit 1)
	@echo "Running go vet..."
	go vet ./...

package: build
	@echo "Creating package..."
	@VERSION=$(VERSION) ./scripts/package.sh

install: check-requirements build
	@echo "Installing $(BINARY_NAME) to /usr/local/bin..."
	sudo cp $(BIN_DIR)/$(BINARY_NAME) /usr/local/bin/
	sudo chmod +x /usr/local/bin/$(BINARY_NAME)
	@echo "✓ Installed: /usr/local/bin/$(BINARY_NAME)"

install-systemd-system:
	@echo "Installing systemd system unit..."
	sudo install -m 644 systemd/virtualcam.service /etc/systemd/system/virtualcam.service
	sudo systemctl daemon-reload
	@echo "✓ Installed: /etc/systemd/system/virtualcam.service"

install-systemd-user:
	@echo "Installing systemd user unit..."
	@mkdir -p ~/.config/systemd/user
	install -m 644 systemd/virtualcam.user.service ~/.config/systemd/user/virtualcam.service
	systemctl --user daemon-reload
	@echo "✓ Installed: ~/.config/systemd/user/virtualcam.service"

uninstall-systemd-system:
	@echo "Removing systemd system unit..."
	sudo systemctl disable --now virtualcam || true
	sudo rm -f /etc/systemd/system/virtualcam.service
	sudo systemctl daemon-reload
	@echo "✓ Removed: /etc/systemd/system/virtualcam.service"

uninstall-systemd-user:
	@echo "Removing systemd user unit..."
	systemctl --user disable --now virtualcam || true
	rm -f ~/.config/systemd/user/virtualcam.service
	systemctl --user daemon-reload
	@echo "✓ Removed: ~/.config/systemd/user/virtualcam.service"
