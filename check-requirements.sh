#!/bin/bash
# Checks the build and runtime dependencies of Virtual Cameraman (Linux)

echo "=== Virtual Cameraman - Dependency Checker ==="
echo ""

MISSING_DEPS=()
WARNINGS=()

# OpenCV: gocv v0.42 needs 4.12.x (OpenCV 5 is not ABI-compatible)
echo "Checking for OpenCV..."
if pkg-config --exists opencv4; then
    VERSION=$(pkg-config --modversion opencv4)
    case "$VERSION" in
        4.12.*) echo "✓ OpenCV $VERSION found" ;;
        *)
            echo "⚠ OpenCV $VERSION found, but gocv v0.42 requires 4.12.x"
            WARNINGS+=("opencv-version")
            ;;
    esac
else
    echo "✗ OpenCV not found"
    MISSING_DEPS+=("opencv")
fi

# ONNX Runtime: loaded at runtime for face detection
echo ""
echo "Checking for ONNX Runtime..."
ORT_LIB="${ONNXRUNTIME_SHARED_LIBRARY_PATH:-}"
if [ -z "$ORT_LIB" ]; then
    for path in /usr/lib/libonnxruntime.so /usr/local/lib/libonnxruntime.so /usr/lib/x86_64-linux-gnu/libonnxruntime.so; do
        if [ -f "$path" ]; then ORT_LIB="$path"; break; fi
    done
fi
if [ -n "$ORT_LIB" ] && [ -f "$ORT_LIB" ]; then
    echo "✓ ONNX Runtime found ($ORT_LIB)"
else
    echo "⚠ ONNX Runtime not found (face detection will be disabled)"
    WARNINGS+=("onnxruntime")
fi

# v4l2loopback
echo ""
echo "Checking for v4l2loopback..."
if [ -f /sys/module/v4l2loopback/version ] || lsmod 2>/dev/null | grep -q v4l2loopback; then
    echo "✓ v4l2loopback kernel module is loaded"
elif modinfo v4l2loopback &> /dev/null; then
    echo "⚠ v4l2loopback is installed but not loaded"
    WARNINGS+=("v4l2loopback-not-loaded")
else
    echo "✗ v4l2loopback not installed"
    MISSING_DEPS+=("v4l2loopback")
fi

# Go
echo ""
echo "Checking for Go..."
if command -v go &> /dev/null; then
    echo "✓ Go found ($(go version | awk '{print $3}'); go.mod requires 1.25+)"
else
    echo "✗ Go not found"
    MISSING_DEPS+=("go")
fi

echo ""
echo "=== Summary ==="

if [ ${#MISSING_DEPS[@]} -eq 0 ] && [ ${#WARNINGS[@]} -eq 0 ]; then
    echo "✓ All dependencies are installed and ready!"
    exit 0
fi

for dep in "${MISSING_DEPS[@]}"; do
    case $dep in
        opencv)
            echo "✗ OpenCV 4.12: distro packages are often a different version."
            echo "    Build it with gocv's installer: https://gocv.io/getting-started/linux/"
            ;;
        v4l2loopback)
            echo "✗ v4l2loopback:"
            echo "    Arch Linux:    sudo pacman -S v4l2loopback-dkms"
            echo "    Ubuntu/Debian: sudo apt install v4l2loopback-dkms"
            ;;
        go)
            echo "✗ Go 1.25+: https://go.dev/doc/install"
            ;;
    esac
done

for warn in "${WARNINGS[@]}"; do
    case $warn in
        opencv-version)
            echo "⚠ Install OpenCV 4.12 (https://gocv.io/getting-started/linux/), or build"
            echo "  and test inside ghcr.io/hybridgroup/opencv:4.12.0"
            ;;
        onnxruntime)
            echo "⚠ ONNX Runtime 1.23.x: https://github.com/microsoft/onnxruntime/releases"
            echo "  Install libonnxruntime.so to /usr/local/lib or set ONNXRUNTIME_SHARED_LIBRARY_PATH"
            ;;
        v4l2loopback-not-loaded)
            echo "⚠ Load the module: sudo modprobe v4l2loopback"
            ;;
    esac
done

# Only hard failures block `make install`
[ ${#MISSING_DEPS[@]} -eq 0 ]
