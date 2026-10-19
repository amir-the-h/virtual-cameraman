# HTTP API

Virtual Cameraman runs a small HTTP API for remote control: Stream Deck buttons,
shell scripts, home automation. It is enabled by default on port `8090`.

```yaml
# config.yaml
api:
  enabled: true
  port: 8090
```

Or on the command line: `-api=false` to disable, `-api-port 9000` to change the port.

## Security model

- The server binds to `127.0.0.1` only and is not reachable from the network.
- There is no authentication; any local process can call it.
- Requests from web pages are rejected. A page open in your browser could
  otherwise reach `localhost`, so the server returns `403` when:
  - the `Host` header is not a loopback address (blocks DNS rebinding), or
  - the request carries a non-local `Origin`, or `Sec-Fetch-Site: cross-site`
    (blocks cross-site requests).

  curl, scripts and Stream Deck / OpenDeck plugins (Node.js) send neither
  header, so they are unaffected.

To control it from another machine, put it behind a reverse proxy that adds
authentication.

## Endpoints

| Method | Path                | Description                          |
| ------ | ------------------- | ------------------------------------ |
| GET    | `/`                 | Lists the endpoints                  |
| GET    | `/health`           | Liveness check                       |
| GET    | `/state`            | Whether capture is running           |
| POST   | `/toggle`           | Start or stop capture                |
| GET    | `/cameras`          | List camera sources                  |
| POST   | `/cameras/switch`   | Switch to a source by ID             |
| POST   | `/cameras/next`     | Switch to the next source (wraps)    |
| POST   | `/cameras/previous` | Switch to the previous source (wraps)|

Wrong methods return `405 Method Not Allowed`.

### GET /state, POST /toggle

```bash
curl http://localhost:8090/state
curl -X POST http://localhost:8090/toggle
```

```json
{ "running": true, "timestamp": "2026-10-19T12:34:56.789Z" }
```

`/toggle` returns the state after toggling. Stopping capture terminates the
pipeline subprocess, so the GPU and model memory is released while idle
(see [Architecture](../README.md#architecture)). Sending `SIGUSR1` to the process
does the same as `/toggle`.

### GET /health

```json
{ "status": "ok", "service": "virtualcam-api" }
```

### Camera endpoints

These need two or more sources under `camera.sources`
([MULTI_CAMERA.md](MULTI_CAMERA.md)) and capture must be running, because the
camera manager lives in the pipeline process. Otherwise they return
`501 Not Implemented`.

```bash
curl http://localhost:8090/cameras
```

```json
{
  "cameras": [
    { "id": 0, "name": "Local Camera 0", "active": true },
    { "id": 1, "name": "RTSP: rtsp://admin:xxxxx@192.168.1.20:554/stream", "active": false }
  ],
  "active_camera": 0,
  "camera_count": 2
}
```

Stream passwords are masked in names, logs and responses.

```bash
curl -X POST http://localhost:8090/cameras/switch -d '{"camera_id": 1}'
curl -X POST http://localhost:8090/cameras/next
curl -X POST http://localhost:8090/cameras/previous
```

```json
{
  "success": true,
  "active_camera": 1,
  "camera_name": "RTSP: rtsp://admin:xxxxx@192.168.1.20:554/stream",
  "message": "Switched to camera 1: RTSP: rtsp://admin:xxxxx@192.168.1.20:554/stream"
}
```

An out-of-range ID returns `400` with `"success": false`; a source that fails
to open returns `500`.

Camera switches last until capture stops. To change the source used at
startup, set `camera.active_source` in the config.

## Examples

### Stream Deck / OpenDeck plugin (Node.js)

```javascript
const res = await fetch("http://localhost:8090/toggle", { method: "POST" });
const { running } = await res.json();
connection.setState(context, running ? 1 : 0); // reflect the real state on the key
```

### Shell

```bash
# Start capture if it is not running
if [ "$(curl -s localhost:8090/state | jq .running)" = "false" ]; then
  curl -s -X POST localhost:8090/toggle
fi

# Show sources, marking the active one
curl -s localhost:8090/cameras | jq -r '.cameras[] | "\(if .active then "*" else " " end) \(.id): \(.name)"'
```

### Home Assistant

```yaml
# configuration.yaml
rest_command:
  virtualcam_toggle:
    url: "http://localhost:8090/toggle"
    method: POST
```

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| `Connection refused` | API disabled, or the process isn't running |
| `bind: address already in use` in the log | Port taken; set `api.port` |
| `501` from `/cameras` | Only one source configured, or capture is stopped |
| `502` from `/cameras` right after `/toggle` | The pipeline is still starting; retry after a second |
| `403 Forbidden` | Request came from a browser page or used a non-local `Host` |
