#!/usr/bin/env bash
# Builds a private OpenCV for virtualcam so the app no longer depends on the
# system OpenCV (which a distro update can move to an incompatible major
# version). gocv v0.42 targets OpenCV 4.12.
#
# Only the modules gocv's core package compiles against are built, CPU only.
# The Makefile picks the result up automatically and embeds an rpath to it.
#
# Environment:
#   OPENCV_VERSION      default 4.12.0
#   OPENCV_PREFIX       default /opt/virtualcam/opencv-$OPENCV_VERSION
#   OPENCV_WORK_DIR     default <repo>/build/opencv (sources + build tree)
#   OPENCV_WITH_FFMPEG  ON/OFF, default OFF. GStreamer (always on) handles the
#                       camera and RTSP pipelines; OpenCV 4.12 does not compile
#                       against FFmpeg 9.x (removed AVCodec fields).
#   JOBS                default $(nproc)
set -euo pipefail

VERSION="${OPENCV_VERSION:-4.12.0}"
PREFIX="${OPENCV_PREFIX:-/opt/virtualcam/opencv-$VERSION}"
REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
WORK="${OPENCV_WORK_DIR:-$REPO_DIR/build/opencv}"
WITH_FFMPEG="${OPENCV_WITH_FFMPEG:-OFF}"
JOBS="${JOBS:-$(nproc)}"

SRC="$WORK/opencv-$VERSION"
BUILD="$WORK/build-$VERSION"
TARBALL="$WORK/opencv-$VERSION.tar.gz"

for tool in cmake ninja curl tar pkg-config; do
    command -v "$tool" >/dev/null || { echo "✗ $tool is required" >&2; exit 1; }
done
pkg-config --exists gstreamer-app-1.0 gstreamer-video-1.0 || {
    echo "✗ GStreamer development files not found (the camera pipelines need them)" >&2
    exit 1
}

mkdir -p "$WORK"

if [ ! -d "$SRC" ]; then
    echo "Downloading OpenCV $VERSION..."
    curl -fL --retry 3 -o "$TARBALL.part" \
        "https://github.com/opencv/opencv/archive/refs/tags/$VERSION.tar.gz"
    mv "$TARBALL.part" "$TARBALL"
    tar -xzf "$TARBALL" -C "$WORK"
fi

# gocv's core package includes these modules (see vendor/gocv.io/x/gocv/*.cpp).
MODULES="core,imgproc,imgcodecs,videoio,highgui,dnn,objdetect,features2d,calib3d,photo,video,flann"

echo "Configuring OpenCV $VERSION -> $PREFIX"
cmake -S "$SRC" -B "$BUILD" -G Ninja \
    -DCMAKE_BUILD_TYPE=Release \
    -DCMAKE_INSTALL_PREFIX="$PREFIX" \
    -DCMAKE_INSTALL_LIBDIR=lib \
    -DCMAKE_INSTALL_RPATH="$PREFIX/lib" \
    -DCMAKE_POLICY_VERSION_MINIMUM=3.5 \
    -DOPENCV_GENERATE_PKGCONFIG=ON \
    -DBUILD_LIST="$MODULES" \
    -DBUILD_SHARED_LIBS=ON \
    -DBUILD_TESTS=OFF -DBUILD_PERF_TESTS=OFF -DBUILD_EXAMPLES=OFF \
    -DBUILD_DOCS=OFF -DBUILD_opencv_apps=OFF -DBUILD_JAVA=OFF \
    -DBUILD_opencv_python2=OFF -DBUILD_opencv_python3=OFF \
    -DBUILD_PROTOBUF=ON -DPROTOBUF_UPDATE_FILES=OFF \
    -DWITH_GSTREAMER=ON -DWITH_FFMPEG="$WITH_FFMPEG" -DWITH_V4L=ON \
    -DWITH_CUDA=OFF -DWITH_OPENCL=OFF -DWITH_OPENGL=OFF \
    -DWITH_GTK=OFF -DWITH_QT=OFF -DWITH_VTK=OFF -DWITH_EIGEN=OFF \
    -DWITH_OPENEXR=OFF -DWITH_OPENJPEG=OFF -DWITH_JASPER=OFF \
    -DWITH_TIFF=OFF -DWITH_WEBP=OFF -DWITH_AVIF=OFF -DWITH_IMGCODEC_HDR=OFF \
    -DWITH_ADE=OFF -DWITH_1394=OFF -DWITH_GPHOTO2=OFF -DWITH_LAPACK=OFF

echo "Building with $JOBS jobs..."
cmake --build "$BUILD" -j "$JOBS"

echo "Installing to $PREFIX"
if mkdir -p "$PREFIX" 2>/dev/null && [ -w "$PREFIX" ]; then
    cmake --install "$BUILD"
else
    sudo cmake --install "$BUILD"
fi

PC="$PREFIX/lib/pkgconfig/opencv4.pc"
[ -f "$PC" ] || { echo "✗ $PC was not installed" >&2; exit 1; }
echo "✓ OpenCV $(PKG_CONFIG_PATH="$PREFIX/lib/pkgconfig" pkg-config --modversion opencv4) installed in $PREFIX ($(du -sh "$PREFIX" | cut -f1))"
echo "  make build will now link against it."
