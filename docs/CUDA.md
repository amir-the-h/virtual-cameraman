# GPU Acceleration

Virtual Cameraman has two independent GPU paths. Only the first matters for
most setups.

| Path | What it accelerates | How to enable |
| --- | --- | --- |
| ONNX Runtime CUDA execution provider | Face detection inference | Automatic, if ONNX Runtime is built with CUDA |
| OpenCV CUDA (`gocv/cuda`) | Resizing the cropped frame to the output size | Build with `make build-cuda` against OpenCV built with CUDA |

Both fall back to the CPU, and the app works without a GPU.

## 1. Inference on the GPU (recommended)

At startup the detector tries the CUDA execution provider and falls back to CPU:

```
ONNX: Using CUDA execution provider
```
or
```
ONNX: CUDA not available, using CPU: ...
```

Requirements:

- NVIDIA driver, CUDA and cuDNN versions supported by your ONNX Runtime release
  (see the [ONNX Runtime CUDA requirements](https://onnxruntime.ai/docs/execution-providers/CUDA-ExecutionProvider.html#requirements))
- The **GPU** build of ONNX Runtime 1.23.x (`onnxruntime-linux-x64-gpu-*.tgz`)
- `ONNXRUNTIME_SHARED_LIBRARY_PATH` pointing at its `libonnxruntime.so`,
  unless it's installed in `/usr/lib` or `/usr/local/lib`

For reference, on CPU the recommended YOLOv11n face model takes about 40 ms per
640×640 inference. Detection runs `detection_fps` times per second (default 5),
not on every frame, so a CPU is usually enough. Between detections the framer
keeps tracking the last known face.

## 2. CUDA resize (optional)

`internal/cuda` wraps OpenCV's CUDA resize for the crop → output step. It sits
behind the `cuda` build tag because it needs OpenCV built with CUDA and the
`cudawarping` module:

```bash
make build-cuda        # go build -tags cuda ...
```

The default build compiles a stub (`internal/cuda/cuda_stub.go`), so CI and
CPU-only machines don't need CUDA headers. The gain is small: one resize per
frame is cheap on a CPU at 720p or 1080p.

### Building OpenCV with CUDA

```bash
git clone --branch 4.12.0 https://github.com/opencv/opencv.git
git clone --branch 4.12.0 https://github.com/opencv/opencv_contrib.git
# Set CUDA_ARCH_BIN to your GPU's compute capability
cmake -S opencv -B opencv/build -D CMAKE_BUILD_TYPE=Release \
  -D OPENCV_EXTRA_MODULES_PATH=opencv_contrib/modules \
  -D WITH_CUDA=ON -D WITH_CUDNN=ON -D OPENCV_DNN_CUDA=ON \
  -D CUDA_ARCH_BIN=8.6 \
  -D OPENCV_GENERATE_PKGCONFIG=ON
cmake --build opencv/build -j"$(nproc)" && sudo cmake --install opencv/build
```

Use OpenCV **4.12**, the version gocv v0.42 targets. OpenCV 5 changed how Mat
types are encoded, and gocv v0.42 does not support it.

## Verifying

- Check the startup log for the execution provider line above.
- Run `nvidia-smi` while capture is running; the process should appear.
- `-log-level debug` logs the inference time of each detection that finds a face.
