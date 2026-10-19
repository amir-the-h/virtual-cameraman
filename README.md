# Virtual Cameraman

[![CI](https://github.com/amir-the-h/virtual-cameraman/actions/workflows/ci.yml/badge.svg)](https://github.com/amir-the-h/virtual-cameraman/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/amir-the-h/virtual-cameraman)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A Linux service that turns any webcam into an auto-framing camera. It finds
your face with a YOLO model, follows it with a smooth, cinematic crop and zoom,
and publishes the result as a virtual webcam you can use in Zoom, Meet, OBS,
Discord or a browser.

<!-- TODO: demo GIF here, raw webcam on the left and the virtual camera on the right -->

## Features

- **Face tracking and auto-framing**: YOLOv11 face detection, an exponential
  moving average on the box, and adaptive zoom that keeps your face at a target
  size as you move closer or further away.
- **GPU or CPU**: ONNX Runtime with the CUDA execution provider, falling back to
  CPU. Detection runs at a configurable rate (default 5/s), and frames in
  between reuse the last tracked position, so a CPU is enough.
- **Virtual camera output** through v4l2loopback (YUYV, MJPEG or BGR4) at any
  output resolution, including a different aspect ratio from the camera.
- **Multiple sources**: local webcams and RTSP streams, switchable at runtime.
- **Background blur or replacement** around the detected face.
- **Headless control**: localhost HTTP API (Stream Deck, scripts, Home
  Assistant), `SIGUSR1` to toggle, systemd units.
- **Live tuning**: framing and detection settings hot-reload from the YAML
  config on save.
- **Zero idle footprint**: when capture is off, the video pipeline process
  exits, so no GPU or model memory is held.

## How it works

```
supervisor process ── HTTP API, SIGUSR1, systemd
   │  starts / stops
   ▼
pipeline subprocess
   webcam / RTSP
     → letterbox to 640×640 → ONNX Runtime (YOLOv11n, CUDA or CPU)
     → confidence filter + NMS → pick main face
     → EMA smoothing + adaptive zoom → crop → resize
     → (optional) background blur / replace
     → v4l2loopback  →  Zoom / Meet / OBS / browser
```

**Framing.** Each detection pass picks the largest face. Its box is smoothed
with an EMA (`box = α·detected + (1−α)·previous`) so the frame glides instead
of jittering. The framer compares the (padded) face height with a target band,
25–50% of the frame by default, and zooms in or out toward it, smoothing the
zoom with its own EMA. The crop is centered on the face, clamped to the frame,
and resized to the output resolution.

**Process model.** Once loaded, OpenCV, ONNX Runtime and CUDA keep a lot of
memory mapped even after you release them, because `dlclose` never truly frees
them. So the long-lived process is a small supervisor: it serves the API and handles
signals. Starting capture re-execs the same binary as a pipeline subprocess
that loads the heavy libraries, and stopping capture terminates it, which lets
the OS reclaim everything. The subprocess watches the config for hot-reload and
serves camera switching on a loopback port, which the supervisor's API proxies
to.

## Requirements

- **Linux** with the `v4l2loopback` kernel module
- **OpenCV 4.12**, the version [gocv](https://gocv.io) v0.42 targets. Distro
  packages are often a different version; OpenCV 5 is not compatible.
  [gocv's installer](https://gocv.io/getting-started/linux/) builds the right
  one.
- **ONNX Runtime 1.23.x**: the CPU build, or the GPU build for CUDA
- **Go 1.25+** to build
- Optional: an NVIDIA GPU with CUDA and cuDNN ([docs/CUDA.md](docs/CUDA.md))

`make check-requirements` checks all of these.

## Quick start

```bash
# 1. Virtual camera device
sudo modprobe v4l2loopback video_nr=10 card_label="Virtual Cameraman" exclusive_caps=1

# 2. ONNX Runtime (CPU build shown; use onnxruntime-linux-x64-gpu-1.23.2.tgz for CUDA)
curl -L https://github.com/microsoft/onnxruntime/releases/download/v1.23.2/onnxruntime-linux-x64-1.23.2.tgz | tar xz
export ONNXRUNTIME_SHARED_LIBRARY_PATH=$PWD/onnxruntime-linux-x64-1.23.2/lib/libonnxruntime.so

# 3. A face detection model (YOLOv11n face, 640x640, ~10 MB)
mkdir -p ~/models
curl -L -o ~/models/model.onnx \
  https://github.com/akanametov/yolo-face/releases/download/1.0.0/yolov11n-face.onnx
export MODEL_DIR=~/models

# 4. Build and run
git clone https://github.com/amir-the-h/virtual-cameraman.git && cd virtual-cameraman
make build
./bin/virtualcam -list-cameras
./bin/virtualcam -camera 0 -virtual-cam-device 10
```

Then pick **Virtual Cameraman** as the camera in your video app. To check the
output without one: `ffplay /dev/video10`.

> The model isn't distributed with this project. The weights linked above come
> from [akanametov/yolo-face](https://github.com/akanametov/yolo-face) (GPL-3.0);
> check that their license fits how you use them. Any single-class YOLOv8/v11
> face model exported to ONNX with a `[1,3,640,640]` input and `[1,5,8400]`
> output works.

## Usage

### Configuration

Copy [`config.yaml.example`](config.yaml.example) to `./config.yaml` or
`~/.config/virtual-cameraman/config.yaml`, or pass `-config <path>`. It
documents every option. Command-line flags override the file.

Keys marked `[live]` (zoom, smoothing, target face size, thresholds,
detection rate, mirror, debug boxes) apply on save while capture is running.
See [docs/CONFIG_HOT_RELOAD.md](docs/CONFIG_HOT_RELOAD.md).

### Common flags

| Flag | Default | Description |
| --- | --- | --- |
| `-camera`, `-c` | `0` | Camera index (`/dev/videoN`) |
| `-width`, `-height`, `-fps` | `1280`, `720`, `60` | Capture mode |
| `-model`, `-m` | from `MODEL_DIR` | ONNX model file or path |
| `-detection-fps`, `-d` | `5` | Detections per second |
| `-zoom-min`, `-zoom-max` | `1.0`, `1.5` | Zoom range |
| `-smoothing`, `-s` | `0.05` | Position EMA α (lower = smoother) |
| `-framing`, `-z` | `true` | Auto-framing; `false` passes the frame through |
| `-virtual-cam-device`, `-vd` | auto-detect | `/dev/videoN` or `N` |
| `-virtual-cam-format`, `-vf` | `YUYV` | `YUYV`, `MJPEG` or `BGR4` |
| `-background`, `-background-mode` | off, `blur` | Background `blur` or `replace` |
| `-api-port`, `-ap` | `8090` | HTTP API port (`-api=false` to disable) |
| `-start-capture`, `-sc` | `true` | Start capturing at launch |
| `-log-level`, `-l` | `info` | `debug` adds per-frame logs |
| `-list-cameras`, `-list-capabilities` | | Inspect devices and exit |
| `-cpuprofile` | | Write a pprof CPU profile |

Run `./bin/virtualcam -help` for the full list. `-h` is the short form of
`-height`.

### Control

```bash
curl -X POST localhost:8090/toggle        # start or stop capture
curl localhost:8090/state                 # {"running": true, ...}
curl -X POST localhost:8090/cameras/next  # next source
kill -USR1 "$(pidof virtualcam)"          # same as /toggle
```

The API listens on localhost only and rejects requests from web pages. See
[docs/API.md](docs/API.md) and [docs/MULTI_CAMERA.md](docs/MULTI_CAMERA.md).

### Running as a service

```bash
make install                    # binary to /usr/local/bin
make install-systemd-user       # ~/.config/systemd/user/virtualcam.service
systemctl --user enable --now virtualcam
```

Edit `Environment=` in the unit to set `MODEL_DIR` and
`ONNXRUNTIME_SHARED_LIBRARY_PATH`. A system-wide unit is also provided
(`make install-systemd-system`).

## Development

```bash
make build        # bin/virtualcam
make build-cuda   # with the OpenCV CUDA resize path (-tags cuda)
make lint         # gofmt check + go vet
make test         # unit tests
```

Tests need OpenCV 4.12. If your system has another version, run them in the
image CI uses:

```bash
docker run --rm -v "$PWD":/src -w /src ghcr.io/hybridgroup/opencv:4.12.0 go test -race ./...
```

An opt-in end-to-end test runs the real model on a photo:

```bash
VIRTUALCAM_TEST_MODEL=~/models/model.onnx VIRTUALCAM_TEST_FACE_IMAGE=portrait.jpg \
  go test ./internal/inference -run RealModel -v
```

### Layout

```
cmd/virtualcam        entry point: supervisor, pipeline subprocess, capture loop
internal/camera       local (V4L2) and RTSP sources, multi-source manager
internal/inference    ONNX Runtime detector, letterbox preprocessing, YOLO decode + NMS
internal/tracking     EMA box smoother
internal/cameraman    main-face selection, adaptive zoom, crop and resize
internal/background   background blur and replacement
internal/virtualcam   v4l2loopback writer and pixel format conversion
internal/api          HTTP API and loopback guard
internal/config       YAML config, defaults, hot-reload watcher
internal/cuda         optional CUDA resize (build tag `cuda`)
```

Contributions are welcome; see [CONTRIBUTING.md](.github/CONTRIBUTING.md).

## Troubleshooting

| Problem | Fix |
| --- | --- |
| `no virtual camera device found` | `sudo modprobe v4l2loopback`, then `v4l2-ctl --list-devices` |
| Apps don't list the camera | Load v4l2loopback with `exclusive_caps=1` (needed by Chrome and WebRTC apps) |
| Purple or garbled output | Match the device format: `v4l2-ctl -d /dev/video10 --get-fmt-video`, then set `-virtual-cam-format` |
| `permission denied` on `/dev/video*` | `sudo usermod -aG video $USER`, then log in again |
| `running without detection` | Set `MODEL_DIR` or `-model`, and make sure ONNX Runtime is found |
| Crashes inside OpenCV, or `scalarToRawData` errors | Your OpenCV isn't 4.12; see Requirements |
| Low FPS | Lower `-detection-fps` or the capture resolution, and check the log for `CUDA execution provider` |

## License

[MIT](LICENSE). Face detection models are not included and carry their own
licenses.

Built on [gocv](https://gocv.io), [ONNX Runtime](https://onnxruntime.ai),
[onnxruntime_go](https://github.com/yalue/onnxruntime_go),
[v4l2loopback](https://github.com/umlaeute/v4l2loopback) and
[Viper](https://github.com/spf13/viper).
