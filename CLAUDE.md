# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**Virtual Cameraman** is a headless Linux service written in Go that automates video framing. It captures webcam or RTSP frames, runs ONNX face detection (CUDA with CPU fallback), crops and stabilizes around the main face, and writes the result to a v4l2loopback virtual camera. It is controlled through an HTTP API, signals and a hot-reloaded YAML config.

## Commands

```bash
make build          # Build bin/virtualcam
make build-cuda     # Build with -tags cuda (OpenCV CUDA resize)
make run            # Build and run
make test           # go test -v ./...
make lint           # gofmt check + go vet
make clean          # Remove bin/ and build artifacts
make check-requirements  # Validate Go, OpenCV, v4l2loopback
gofmt -w <file>     # Format a file

# Single package or test:
go test ./internal/config
go test ./internal/cameraman -run TestCalculateCropRegion
go test ./internal/config -run TestLoadFromFile_ValidYAML -count=1

# Opt-in end-to-end detector test (needs ONNX Runtime + model):
VIRTUALCAM_TEST_MODEL=/path/yolov11n-face.onnx VIRTUALCAM_TEST_FACE_IMAGE=/path/face.jpg \
  go test ./internal/inference -run RealModel -v
```

Tests require OpenCV **4.12** (what gocv v0.42 targets). OpenCV 5 is ABI-incompatible with gocv v0.42 (Mat type constants changed). On such hosts, run tests in `ghcr.io/hybridgroup/opencv:4.12.0`, the image CI uses.

**Environment variables:**
- `MODEL_DIR`: directory for ONNX model files (prefers `model_gpu.onnx`, then `model.onnx`); falls back to `ONNXRUNTIME_HOME`
- `ONNXRUNTIME_SHARED_LIBRARY_PATH`: path to `libonnxruntime.so` if it isn't in `/usr/lib` or `/usr/local/lib`

Verbose per-frame logging: `-log-level debug` (or `log.level: debug` in config).

## Architecture

### Process model

The main process is a lightweight supervisor: it runs the API server and handles signals. Starting capture (`POST /toggle`, `SIGUSR1`, or `start_at_launch`) re-execs the same binary with `--pipeline-subprocess`. All heavy libraries (OpenCV, ONNX Runtime, CUDA) live in that child, so stopping capture returns all its memory to the OS. Consequences:
- The **child** watches the config file for hot-reload (`watchConfig` in `cmd/virtualcam/main.go`).
- The **child** owns the camera manager and serves the camera endpoints on `127.0.0.1:<api_port+1>` (`api.Server.StartCameraControl`). The parent's `/cameras*` handlers reverse-proxy there (`proxyToPipeline`).

### Frame Processing Pipeline

```
Capture → Preprocess → Infer → Post-process → Select → Smooth → Frame → Output
```

1. `internal/camera`: webcam/RTSP capture; `Manager` for multi-source switching
2. `internal/inference`: ONNX model loading, `Detector` interface (`OnnxDetector`, `FallbackDetector`, stub); tries CUDA then CPU
3. `internal/tracking`: `Smoother`, an EMA on face bounding box coordinates (`newBox = α*detected + (1-α)*prev`); not thread-safe, guarded by the Framer's mutex
4. `internal/cameraman`: `Framer`: `SelectMainFace` (largest box), adaptive zoom, `CalculateCropRegion` (must stay within frame bounds, even when the output aspect ratio differs from the input), `CropAndResize` (optional CUDA resize)
5. `internal/background`: background blur/replacement using a face mask
6. `internal/virtualcam`: v4l2loopback output (YUYV/MJPEG/BGR4); resizes to the configured output size
7. `internal/api`: HTTP API (state, toggle, cameras); localhost only, with a `localOnly` guard against browser CSRF and DNS rebinding
8. `internal/config`: Viper-based config (`config.New()` defaults); `ResolvePath` handles the default locations; `WatchConfig` watches the parent directory (survives atomic-rename saves); use `cfg.View`/`cfg.Update` for fields that hot-reload
9. `internal/logger`: structured logging helpers (`logger.Infof`, etc.)
10. `internal/cuda`: optional CUDA image resize behind the `cuda` build tag (stub otherwise)

### Key Patterns

- **Interfaces**: Small, focused interfaces at package level or in `*interface.go`. They make backends swappable and allow mocks in tests (no real GPU in unit tests).
- **Config**: All tunable params (smoothing α, zoom range, thresholds, FPS) are exposed via config; keep `config.yaml.example` in sync (a test loads it). Mark hot-reloadable keys `[live]` there.
- **GPU awareness**: CUDA provider attempted first, CPU fallback logged; inference timing reported per frame.
- **Concurrency**: `context.Context` for cancellation in long-running loops. Per-frame reads of hot-reloadable config go through a single `cfg.View` snapshot.
- **Hot loops**: Minimize allocations and `gocv.Mat` copies in per-frame paths; profile with `-cpuprofile` / `pprof`.
- **Error wrapping**: `fmt.Errorf("action: %w", err)` pattern throughout.
- **Secrets**: Never log or return RTSP URLs directly; use `camera.RedactURL`.

### YOLOv11n Post-Processing

Model input `[1,3,640,640]`, output `[1,5,8400]` (cx, cy, w, h, conf). Always: confidence threshold (~0.5) → NMS (IoU ~0.4) → largest-box selection.

## Commit Convention

Conventional commits: `type(scope): summary` (≤50 chars, lowercase).

Scopes: `inference`, `tracking`, `cameraman`, `virtualcam`, `config`, `camera`, `api`, `background`, `ci`, `docs`.

Types: `feat`, `fix`, `refactor`, `test`, `docs`, `chore`.

Examples: `feat(inference): add cuda fallback`, `fix(tracking): tighten nms thresholding`

PR titles follow the same format; include testing notes in the description.

## Dependencies

- `gocv.io/x/gocv v0.42.0`: OpenCV bindings (requires OpenCV 4.12 system libs)
- `github.com/yalue/onnxruntime_go v1.23.0`: ONNX Runtime bindings (requires ONNX Runtime 1.23.x; GPU build for CUDA)
- `github.com/spf13/viper v1.21.0`: configuration
- `github.com/fsnotify/fsnotify v1.9.0`: config file watching
- Linux: `v4l2loopback` kernel module (`sudo modprobe v4l2loopback`)
