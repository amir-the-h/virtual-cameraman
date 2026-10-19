# Multiple Camera Sources

You can configure several sources, local webcams and RTSP streams, and switch
between them at runtime through the [HTTP API](API.md).

## Configuration

```yaml
camera:
  sources:
    - type: local          # V4L2 webcam
      device_id: 0         # /dev/video0
      width: 1920          # optional; defaults to camera.width/height/fps/format
      height: 1080
      fps: 30
      format: "MJPG"       # optional FOURCC: YUYV, MJPG, H264
      mirror: true         # optional; flip horizontally (hot-reloads)
      manual_exposure: 0   # optional; >0 locks exposure_time_absolute

    - type: rtsp
      url: "rtsp://user:password@camera.local:554/stream"

  active_source: 0         # source used when capture starts (0-based)
```

If `sources` is empty, the app uses a single local camera from the top-level
`camera.index`, `width`, `height`, `fps` and `format` keys.

## Switching

```bash
curl http://localhost:8090/cameras                                   # list
curl -X POST http://localhost:8090/cameras/next                      # cycle forward
curl -X POST http://localhost:8090/cameras/previous                  # cycle back
curl -X POST http://localhost:8090/cameras/switch -d '{"camera_id": 1}'
```

The camera endpoints work while capture is running and need at least two
sources. See [API.md](API.md#camera-endpoints).

## Behavior

- Only the active source is open, so inactive webcams and streams use no
  bandwidth or CPU.
- A switch opens the new source first, then closes the old one. If the new
  source fails to open, the old one stays active and the API returns the error.
  Expect a short pause in the output (longer for RTSP while the stream connects).
- Sources may have different resolutions. The framer picks up the new input size
  on the next frame, and the virtual camera keeps its configured output size.
- To reconnect a dropped RTSP stream, switch away and back.

## RTSP notes

- Streams are opened through a GStreamer pipeline
  (`rtspsrc latency=100 ! decodebin ! videoconvert ! appsink`), falling back to
  OpenCV's built-in RTSP support if GStreamer isn't available.
- Credentials in the URL are masked (`user:xxxxx@`) in logs and API responses.
- Expect some extra latency compared with a local webcam, depending on the
  network, codec and the camera's own buffering.

## Troubleshooting

**Stream won't connect.** Check it plays outside the app, and that GStreamer has
the RTSP plugin:

```bash
ffplay rtsp://camera.local:554/stream
gst-inspect-1.0 rtspsrc
```

**Switching returns 501.** Fewer than two sources are configured, or capture is
stopped.

Run with `-log-level debug` for detailed open, close and switch logs.

## Implementation

```
camera.Manager            owns the sources; Read() always serves the active one
 ├── LocalSource          gocv.VideoCapture on /dev/videoN (V4L2)
 └── RTSPSource           gocv.VideoCapture via GStreamer
```

Every source implements `camera.Source` (`Open`, `Read`, `Close`, `IsOpened`,
`Name`, `Width`, `Height`, `FPS`), so a new kind of source only needs that
interface.
