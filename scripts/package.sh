#!/bin/bash
# Package Virtual Cameraman for distribution
# Creates a tar.gz archive with the binary and necessary files

set -e

VERSION=${VERSION:-"1.0.0"}
BUILD_DIR="build"
DIST_DIR="${BUILD_DIR}/virtual-cameraman-${VERSION}"
ARCH=$(uname -m)
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
BINARY_NAME="virtualcam"
ARCHIVE_NAME="virtual-cameraman-${VERSION}-${OS}-${ARCH}.tar.gz"

echo "Packaging Virtual Cameraman ${VERSION} for ${OS}-${ARCH}..."

# Clean and create directories
rm -rf "${BUILD_DIR}"
mkdir -p "${DIST_DIR}"

# Build binary
echo "Building binary..."
make build

# Copy binary
cp "bin/${BINARY_NAME}" "${DIST_DIR}/"

# Copy documentation
cp README.md "${DIST_DIR}/"
cp config.yaml.example "${DIST_DIR}/config.yaml.example"

# Create install script
cat > "${DIST_DIR}/install.sh" << 'EOF'
#!/bin/bash
# Simple install script for Virtual Cameraman

set -e

BINARY_NAME="virtualcam"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
CONFIG_DIR="${HOME}/.config/virtual-cameraman"

echo "Installing Virtual Cameraman..."

# Install binary
sudo cp "${BINARY_NAME}" "${INSTALL_DIR}/"
sudo chmod +x "${INSTALL_DIR}/${BINARY_NAME}"

# Create config directory
mkdir -p "${CONFIG_DIR}"

# Copy example config if it doesn't exist
if [ ! -f "${CONFIG_DIR}/config.yaml" ]; then
    cp config.yaml.example "${CONFIG_DIR}/config.yaml"
    echo "Created example config at ${CONFIG_DIR}/config.yaml"
fi

echo "Installation complete!"
echo "Binary installed to: ${INSTALL_DIR}/${BINARY_NAME}"
echo "Config directory: ${CONFIG_DIR}"
echo ""
echo "To use Virtual Cameraman:"
echo "  ${INSTALL_DIR}/${BINARY_NAME} -camera 0"
EOF

chmod +x "${DIST_DIR}/install.sh"

# Create README for the package
cat > "${DIST_DIR}/INSTALL.txt" << EOF
Virtual Cameraman ${VERSION}
============================

Quick Install:
  ./install.sh

Manual Install:
  1. Copy virtualcam to /usr/local/bin/ (or another directory in your PATH)
  2. Make it executable: chmod +x virtualcam
  3. (Optional) Copy config.yaml.example to ~/.config/virtual-cameraman/config.yaml

Requirements:
  - Linux with v4l2loopback installed
  - OpenCV 4 libraries
  - ONNX Runtime (optional, for face detection)

For detailed setup instructions, see README.md
EOF

# Create archive
echo "Creating archive..."
cd "${BUILD_DIR}"
tar -czf "${ARCHIVE_NAME}" "virtual-cameraman-${VERSION}/"
cd ..

echo "Package created: ${BUILD_DIR}/${ARCHIVE_NAME}"
echo ""
echo "To install:"
echo "  cd ${BUILD_DIR}"
echo "  tar -xzf ${ARCHIVE_NAME}"
echo "  cd virtual-cameraman-${VERSION}"
echo "  ./install.sh"

