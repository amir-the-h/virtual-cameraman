# Config Hot-Reload

While capture is running, Virtual Cameraman watches its config file and applies
framing and detection settings on the next frame. Tuning zoom or smoothing
doesn't need a restart: edit `config.yaml`, save, and watch the output.

## Which file is watched

The same file the app loaded:

1. the path given with `-config` / `-C`, or
2. `./config.yaml`, or
3. `~/.config/virtual-cameraman/config.yaml`.

If none exists, nothing is watched and built-in defaults are used.

## What reloads live

These are marked `[live]` in [`config.yaml.example`](../config.yaml.example):

| Section     | Keys |
| ----------- | ---- |
| `cameraman` | `smoothing_factor`, `zoom_min`, `zoom_max`, `target_face_size_min`, `target_face_size_max`, `zoom_smoothing_factor`, `face_box_padding` |
| `inference` | `confidence_threshold`, `nms_threshold`, `detection_fps` |
| `camera.sources[]` | `mirror` |
| `debug`     | `show_boxes` |

Everything else (camera resolution and format, model, background processing,
virtual camera device and format, API port) is read when capture starts. To
apply those, stop and start capture with `POST /toggle` (or `SIGUSR1`) twice;
the process and API server keep running.

## How it works

- The pipeline runs in a child process (see [Architecture](../README.md#architecture)),
  and that child watches the file with [fsnotify](https://github.com/fsnotify/fsnotify).
- It watches the file's **directory**, not the file. Many editors (vim, VS Code,
  and others) save by writing a temp file and renaming it over the original;
  a watch on the file itself would silently stop after the first save.
- On a change, the file is parsed into a fresh `Config`. If it fails to parse,
  the error is logged and the previous settings stay in effect.
- New values are copied into the shared config under a `sync.RWMutex`, and the
  framer's parameters are updated under its own lock. The capture loop reads a
  snapshot of the live settings once per frame, so a frame never mixes old and
  new values.

The behavior is covered by `TestWatchConfig_ReloadsOnAtomicRename` in
`internal/config`.

## Tuning tip

Set `debug.show_boxes: true` while tuning. The output then shows every detected
face (green) and the crop region (blue), so you can see how `face_box_padding`
and the target face size change the framing.
