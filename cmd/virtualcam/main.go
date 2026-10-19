package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/color"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/amir-the-h/virtual-cameraman/internal/api"
	"github.com/amir-the-h/virtual-cameraman/internal/background"
	"github.com/amir-the-h/virtual-cameraman/internal/camera"
	"github.com/amir-the-h/virtual-cameraman/internal/cameraman"
	"github.com/amir-the-h/virtual-cameraman/internal/config"
	"github.com/amir-the-h/virtual-cameraman/internal/inference"
	"github.com/amir-the-h/virtual-cameraman/internal/logger"
	"github.com/amir-the-h/virtual-cameraman/internal/virtualcam"
	"gocv.io/x/gocv"
)

func resolveVirtualCameraDevice(device string) (string, error) {
	if filepath.IsAbs(device) {
		if _, err := os.Stat(device); err == nil {
			return device, nil
		}
		return "", fmt.Errorf("device path does not exist: %s", device)
	}

	if index, err := strconv.Atoi(device); err == nil {
		devicePath := fmt.Sprintf("/dev/video%d", index)
		if _, err := os.Stat(devicePath); err == nil {
			return devicePath, nil
		}
		return "", fmt.Errorf("device index %d does not exist: %s", index, devicePath)
	}

	return "", fmt.Errorf("invalid device specification: %s (use index like '2' or path like '/dev/video2')", device)
}

func parseHexColor(hex string) (color.RGBA, error) {
	if len(hex) > 0 && hex[0] == '#' {
		hex = hex[1:]
	}

	switch hex {
	case "black":
		return color.RGBA{R: 0, G: 0, B: 0, A: 255}, nil
	case "white":
		return color.RGBA{R: 255, G: 255, B: 255, A: 255}, nil
	case "blue":
		return color.RGBA{R: 0, G: 0, B: 255, A: 255}, nil
	case "green":
		return color.RGBA{R: 0, G: 255, B: 0, A: 255}, nil
	case "red":
		return color.RGBA{R: 255, G: 0, B: 0, A: 255}, nil
	}

	if len(hex) != 6 {
		return color.RGBA{}, fmt.Errorf("invalid hex color: %s (expected 6 hex digits)", hex)
	}

	var r, g, b uint8
	_, err := fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("invalid hex color: %s", hex)
	}

	return color.RGBA{R: r, G: g, B: b, A: 255}, nil
}

// watchConfig reloads tunable settings into cfg (and the active framer, if any)
// whenever configFile changes. Returns a stop function, or nil if watching failed.
func watchConfig(configFile string, cfg *config.Config, activeFramer *atomic.Pointer[cameraman.Framer]) func() {
	stop, err := config.WatchConfig(configFile, func(newCfg *config.Config) {
		log.Println("Config reloaded")
		cfg.Update(func(c *config.Config) {
			c.ConfidenceThreshold = newCfg.ConfidenceThreshold
			c.NMSThreshold = newCfg.NMSThreshold
			c.DetectionFPS = newCfg.DetectionFPS
			c.SmoothingFactor = newCfg.SmoothingFactor
			c.ZoomMin = newCfg.ZoomMin
			c.ZoomMax = newCfg.ZoomMax
			c.TargetFaceSizeMin = newCfg.TargetFaceSizeMin
			c.TargetFaceSizeMax = newCfg.TargetFaceSizeMax
			c.ZoomSmoothingFactor = newCfg.ZoomSmoothingFactor
			c.FaceBoxPadding = newCfg.FaceBoxPadding
			c.ShowBoxes = newCfg.ShowBoxes
			c.CameraSources = newCfg.CameraSources
		})
		if f := activeFramer.Load(); f != nil {
			f.UpdateParameters(
				newCfg.SmoothingFactor, newCfg.ZoomMin, newCfg.ZoomMax,
				newCfg.TargetFaceSizeMin, newCfg.TargetFaceSizeMax,
				newCfg.ZoomSmoothingFactor, newCfg.FaceBoxPadding,
			)
		}
	})
	if err != nil {
		log.Printf("Warning: could not watch config file: %v", err)
		return nil
	}
	log.Printf("Watching config: %s", configFile)
	return stop
}

// runPipeline initializes all video components and runs the capture loop.
// Returns when ctx is cancelled. All resources are released before returning.
func runPipeline(
	ctx context.Context,
	cfg *config.Config,
	enableFraming bool,
	activeFramer *atomic.Pointer[cameraman.Framer],
	apiServer *api.Server,
) {
	log.Println("Pipeline starting: initializing video components...")

	// --- Camera manager ---
	var camManager *camera.Manager
	if len(cfg.CameraSources) > 0 {
		log.Printf("Using multi-source camera configuration (%d source(s))", len(cfg.CameraSources))
		sources := make([]camera.Source, len(cfg.CameraSources))
		for i, srcCfg := range cfg.CameraSources {
			switch srcCfg.Type {
			case "local":
				w := srcCfg.Width
				if w == 0 {
					w = cfg.Width
				}
				h := srcCfg.Height
				if h == 0 {
					h = cfg.Height
				}
				fps := srcCfg.FPS
				if fps == 0 {
					fps = cfg.FPS
				}
				format := srcCfg.Format
				if format == "" {
					format = cfg.Format
				}
				sources[i] = camera.NewLocalSource(srcCfg.DeviceID, w, h, fps, format)
				log.Printf("  Source %d: Local camera %d (%dx%d @ %d FPS)", i, srcCfg.DeviceID, w, h, fps)
			case "rtsp":
				if srcCfg.URL == "" {
					log.Printf("RTSP source %d missing URL, skipping", i)
					continue
				}
				sources[i] = camera.NewRTSPSource(srcCfg.URL)
				log.Printf("  Source %d: RTSP stream %s", i, camera.RedactURL(srcCfg.URL))
			default:
				log.Printf("Unknown camera source type: %s (source %d), skipping", srcCfg.Type, i)
			}
		}
		var err error
		camManager, err = camera.NewManager(sources)
		if err != nil {
			log.Printf("Pipeline: failed to initialize camera manager: %v", err)
			return
		}
		if cfg.ActiveSource > 0 && cfg.ActiveSource < len(sources) {
			if err := camManager.SwitchTo(cfg.ActiveSource); err != nil {
				log.Printf("Warning: failed to switch to active source %d: %v", cfg.ActiveSource, err)
			}
		}
	} else {
		log.Printf("Using single-camera mode (camera index %d)", cfg.CameraIndex)
		localSource := camera.NewLocalSource(cfg.CameraIndex, cfg.Width, cfg.Height, cfg.FPS, cfg.Format)
		var err error
		camManager, err = camera.NewManager([]camera.Source{localSource})
		if err != nil {
			log.Printf("Pipeline: failed to initialize camera: %v", err)
			return
		}
	}
	defer camManager.Close()

	if len(cfg.CameraSources) > 0 {
		idx := cfg.ActiveSource
		if idx < 0 || idx >= len(cfg.CameraSources) {
			idx = 0
		}
		srcCfg := cfg.CameraSources[idx]
		if srcCfg.Type == "local" && srcCfg.ManualExposure > 0 {
			if err := camera.SetManualExposure(srcCfg.DeviceID, srcCfg.ManualExposure); err != nil {
				log.Printf("Warning: failed to set manual exposure on camera %d: %v", srcCfg.DeviceID, err)
			}
		}
	}

	activeSource := camManager.GetActive()
	log.Printf("Active camera: %s (%dx%d @ %d FPS)",
		activeSource.Name(), activeSource.Width(), activeSource.Height(), activeSource.FPS())

	targetFPS := cfg.FPS
	actualFPS := activeSource.FPS()
	if actualFPS < targetFPS {
		targetFPS = actualFPS
		log.Printf("Camera FPS (%d) < requested (%d), using camera FPS", actualFPS, cfg.FPS)
	} else if actualFPS > targetFPS {
		log.Printf("Camera running at %d FPS, throttling to %d FPS", actualFPS, targetFPS)
	}
	if targetFPS <= 0 {
		targetFPS = 30
	}

	detectionInterval := cfg.CalculateDetectionInterval(targetFPS)
	if detectionInterval > 0 {
		log.Printf("Detection interval: every %d frames (%.1f detections/sec at %d FPS)",
			detectionInterval, cfg.DetectionFPS, targetFPS)
	}

	// --- Register camera functions with API server ---
	if apiServer != nil && camManager.SourceCount() > 1 {
		getCameras := func() ([]string, int) {
			return camManager.ListSources(), camManager.GetActiveIndex()
		}
		switchCamera := func(idx int) error { return camManager.SwitchTo(idx) }
		nextCamera := func() error {
			cur := camManager.GetActiveIndex()
			return camManager.SwitchTo((cur + 1) % camManager.SourceCount())
		}
		prevCamera := func() error {
			cur := camManager.GetActiveIndex()
			return camManager.SwitchTo((cur - 1 + camManager.SourceCount()) % camManager.SourceCount())
		}
		apiServer.SetCameraFunctions(getCameras, switchCamera, nextCamera, prevCamera)
		defer apiServer.SetCameraFunctions(nil, nil, nil, nil)
	}

	// --- Framer ---
	var framer *cameraman.Framer
	if enableFraming {
		src := camManager.GetActive()
		outW, outH := src.Width(), src.Height()
		if cfg.VirtualCamOutputWidth > 0 {
			outW = cfg.VirtualCamOutputWidth
		}
		if cfg.VirtualCamOutputHeight > 0 {
			outH = cfg.VirtualCamOutputHeight
		}
		framer = cameraman.NewFramer(
			src.Width(), src.Height(),
			outW, outH,
			cfg.SmoothingFactor,
			cfg.ZoomMin, cfg.ZoomMax,
			cfg.TargetFaceSizeMin, cfg.TargetFaceSizeMax,
			cfg.ZoomSmoothingFactor,
			cfg.FaceBoxPadding,
		)
		activeFramer.Store(framer)
		defer activeFramer.Store(nil)
		log.Printf("Framer: input=%dx%d output=%dx%d smoothing=%.2f, zoom=%.1f-%.1f, face=%.1f%%-%.1f%%, zoom-smooth=%.2f, padding=%.2f",
			src.Width(), src.Height(), outW, outH,
			cfg.SmoothingFactor, cfg.ZoomMin, cfg.ZoomMax,
			cfg.TargetFaceSizeMin*100, cfg.TargetFaceSizeMax*100,
			cfg.ZoomSmoothingFactor, cfg.FaceBoxPadding)
	}

	// --- Background processor ---
	var bgProcessor *background.Processor
	if cfg.BackgroundEnabled {
		bgColor, err := parseHexColor(cfg.BackgroundColor)
		if err != nil {
			log.Printf("Warning: invalid background color '%s', using black: %v", cfg.BackgroundColor, err)
			bgColor = color.RGBA{R: 0, G: 0, B: 0, A: 255}
		}
		bgProcessor, err = background.NewProcessor(
			cfg.BackgroundEnabled,
			cfg.BackgroundMode,
			cfg.BackgroundBlurStrength,
			cfg.BackgroundImagePath,
			bgColor,
			cfg.BackgroundFacePadding,
		)
		if err != nil {
			log.Printf("Warning: failed to initialize background processor: %v", err)
		} else {
			defer bgProcessor.Close()
			log.Printf("Background processor: mode=%s, blur=%d, padding=%.2f",
				cfg.BackgroundMode, cfg.BackgroundBlurStrength, cfg.BackgroundFacePadding)
		}
	}

	// --- Detector ---
	var detector inference.Detector
	var onnxDet *inference.OnnxDetector
	if cfg.ModelPath != "" && !cfg.NoDetection {
		factory := inference.NewDetectorFactory(
			cfg.ModelPath,
			cfg.ConfidenceThreshold,
			cfg.InferenceWidth,
			cfg.InferenceHeight,
		)
		var err error
		detector, err = factory.CreateDetector()
		if err != nil {
			log.Printf("Warning: failed to create detector: %v, running without detection", err)
		} else {
			defer detector.Close()
			if d, ok := detector.(*inference.OnnxDetector); ok {
				onnxDet = d
				onnxDet.SetNMSThreshold(cfg.NMSThreshold)
				onnxDet.SetConfidenceThreshold(cfg.ConfidenceThreshold)
			}
			log.Printf("Detector: %s", detector.GetExecutionProvider())
		}
	} else if cfg.NoDetection {
		log.Println("Face detection disabled (--no-detection)")
	} else {
		log.Println("No model path; skipping detection")
	}

	// --- Virtual camera ---
	var vcam virtualcam.VirtualCamera
	if cfg.VirtualCamEnabled {
		devicePath := cfg.VirtualCamDevice
		if devicePath == "" {
			detected, err := virtualcam.FindAvailableVirtualCamera()
			if err != nil {
				log.Printf("Warning: auto-detect virtual camera failed: %v", err)
			} else {
				devicePath = detected
				log.Printf("Auto-detected virtual camera: %s", devicePath)
			}
		} else {
			resolved, err := resolveVirtualCameraDevice(devicePath)
			if err != nil {
				log.Printf("Warning: resolve virtual camera device '%s': %v", devicePath, err)
				devicePath = ""
			} else {
				devicePath = resolved
			}
		}

		if devicePath != "" {
			src := camManager.GetActive()
			outW, outH := src.Width(), src.Height()
			if framer != nil {
				outW = framer.OutputWidth()
				outH = framer.OutputHeight()
			}
			var err error
			vcam, err = virtualcam.NewLinuxVirtualCamera(devicePath, outW, outH, cfg.VirtualCamFormat)
			if err != nil {
				log.Printf("Warning: failed to initialize virtual camera: %v", err)
			} else {
				defer vcam.Close()
				log.Printf("Virtual camera: %s (%dx%d, %s)", vcam.GetDevicePath(), outW, outH, cfg.VirtualCamFormat)
			}
		}
	}

	log.Println("Pipeline ready. Starting capture loop.")

	// --- Capture loop ---
	targetFrameTime := time.Duration(float64(1000)/float64(targetFPS)) * time.Millisecond
	nextFrameTime := time.Now().Add(targetFrameTime)

	frameCount := 0
	lastMetricsTime := time.Now()
	var lastDetections []inference.BoundingBox
	lastFrameWidth, lastFrameHeight := 0, 0
	appliedConfidence, appliedNMS := cfg.ConfidenceThreshold, cfg.NMSThreshold

	for {
		select {
		case <-ctx.Done():
			log.Println("Pipeline stopping.")
			return
		default:
		}

		now := time.Now()
		if now.Before(nextFrameTime) {
			select {
			case <-ctx.Done():
				log.Println("Pipeline stopping.")
				return
			case <-time.After(nextFrameTime.Sub(now)):
			}
		}
		nextFrameTime = nextFrameTime.Add(targetFrameTime)
		if now.After(nextFrameTime.Add(targetFrameTime)) {
			nextFrameTime = now.Add(targetFrameTime)
		}

		// Snapshot settings that config hot-reload may change
		var showBoxes, noDetection bool
		var detectionFPS, confidence, nms float32
		var cameraSources []config.CameraSourceConfig
		cfg.View(func(c *config.Config) {
			showBoxes = c.ShowBoxes
			noDetection = c.NoDetection
			detectionFPS = c.DetectionFPS
			confidence = c.ConfidenceThreshold
			nms = c.NMSThreshold
			cameraSources = c.CameraSources
		})
		if onnxDet != nil && (confidence != appliedConfidence || nms != appliedNMS) {
			onnxDet.SetConfidenceThreshold(confidence)
			onnxDet.SetNMSThreshold(nms)
			appliedConfidence, appliedNMS = confidence, nms
		}

		frame, err := camManager.Read()
		if err != nil {
			log.Printf("Failed to read frame: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}

		if frame.Empty() || frame.Cols() == 0 || frame.Rows() == 0 {
			logger.Debugf("Skipping empty frame")
			continue
		}

		activeIdx := camManager.GetActiveIndex()
		if activeIdx >= 0 && activeIdx < len(cameraSources) && cameraSources[activeIdx].Mirror {
			gocv.Flip(frame, &frame, 1)
		}

		if framer != nil && frame.Cols() > 0 && frame.Rows() > 0 {
			curW, curH := frame.Cols(), frame.Rows()
			if lastFrameWidth > 0 && lastFrameHeight > 0 && (curW != lastFrameWidth || curH != lastFrameHeight) {
				log.Printf("Resolution changed %dx%d -> %dx%d, updating framer", lastFrameWidth, lastFrameHeight, curW, curH)
				framer.UpdateDimensions(curW, curH)
			}
			lastFrameWidth, lastFrameHeight = curW, curH
		}

		var detections []inference.BoundingBox
		var dynInterval int
		if detectionFPS <= 0 {
			dynInterval = 0
		} else if targetFPS <= 0 {
			dynInterval = 1
		} else {
			iv := float32(targetFPS) / detectionFPS
			if iv < 1.0 {
				dynInterval = 0
			} else {
				dynInterval = int(iv)
			}
		}

		if detector != nil && (dynInterval == 0 || frameCount%dynInterval == 0) {
			boxes, err := detector.DetectFaces(frame)
			if err != nil {
				log.Printf("Detection error: %v", err)
			} else {
				detections = boxes
				lastDetections = boxes
				if len(boxes) > 0 {
					logger.Debugf("Detected %d face(s), %.2f ms", len(boxes), detector.GetInferenceTime())
				}
			}
		}

		displayFrame := frame
		var croppedFrame gocv.Mat
		hasCroppedFrame := false
		var cropOffsetX, cropOffsetY int
		var targetFace *inference.BoundingBox
		var cropRegion image.Rectangle

		if framer != nil {
			use := detections
			if len(use) == 0 {
				use = lastDetections
			}
			if len(use) > 0 {
				targetFace = framer.Update(use)
				if targetFace != nil {
					cropRegion, err = framer.CalculateCropRegion(targetFace)
					if err == nil {
						cropped, err := framer.CropAndResize(frame, cropRegion)
						if err == nil {
							displayFrame = cropped
							croppedFrame = cropped
							hasCroppedFrame = true
							cropOffsetX = cropRegion.Min.X
							cropOffsetY = cropRegion.Min.Y
						}
					}
				}
			}
		}

		hasBgProcessedFrame := false
		if bgProcessor != nil {
			use := detections
			if len(use) == 0 {
				use = lastDetections
			}
			adjusted := make([]inference.BoundingBox, len(use))
			for i, box := range use {
				if hasCroppedFrame {
					sx := float32(displayFrame.Cols()) / float32(frame.Cols())
					sy := float32(displayFrame.Rows()) / float32(frame.Rows())
					adjusted[i] = inference.BoundingBox{
						X1:         (box.X1 - float32(cropOffsetX)) * sx,
						Y1:         (box.Y1 - float32(cropOffsetY)) * sy,
						X2:         (box.X2 - float32(cropOffsetX)) * sx,
						Y2:         (box.Y2 - float32(cropOffsetY)) * sy,
						Confidence: box.Confidence,
					}
				} else {
					adjusted[i] = box
				}
			}
			processed, err := bgProcessor.Process(displayFrame, adjusted)
			if err != nil {
				log.Printf("Background processing error: %v", err)
			} else {
				if hasCroppedFrame {
					displayFrame.Close()
					hasCroppedFrame = false
				} else if hasBgProcessedFrame {
					displayFrame.Close()
				}
				displayFrame = processed
				hasBgProcessedFrame = true
			}
		}

		if showBoxes && !noDetection && detector != nil {
			boxes := detections
			if len(boxes) == 0 {
				boxes = lastDetections
			}
			if len(boxes) > 0 {
				sx := float32(displayFrame.Cols()) / float32(frame.Cols())
				sy := float32(displayFrame.Rows()) / float32(frame.Rows())
				for _, box := range boxes {
					ax1 := (box.X1 - float32(cropOffsetX)) * sx
					ay1 := (box.Y1 - float32(cropOffsetY)) * sy
					ax2 := (box.X2 - float32(cropOffsetX)) * sx
					ay2 := (box.Y2 - float32(cropOffsetY)) * sy
					if ax1 < 0 {
						ax1 = 0
					}
					if ay1 < 0 {
						ay1 = 0
					}
					if ax2 > float32(displayFrame.Cols()) {
						ax2 = float32(displayFrame.Cols())
					}
					if ay2 > float32(displayFrame.Rows()) {
						ay2 = float32(displayFrame.Rows())
					}
					if ax2 > ax1 && ay2 > ay1 {
						gocv.Rectangle(&displayFrame, image.Rect(int(ax1), int(ay1), int(ax2), int(ay2)),
							color.RGBA{R: 0, G: 255, B: 0, A: 255}, 2)
					}
				}
				if framer != nil && targetFace != nil {
					if hasCroppedFrame {
						gocv.Rectangle(&displayFrame,
							image.Rect(5, 5, displayFrame.Cols()-5, displayFrame.Rows()-5),
							color.RGBA{R: 0, G: 128, B: 255, A: 255}, 3)
					} else {
						crX1 := float32(cropRegion.Min.X-cropOffsetX) * sx
						crY1 := float32(cropRegion.Min.Y-cropOffsetY) * sy
						crX2 := float32(cropRegion.Max.X-cropOffsetX) * sx
						crY2 := float32(cropRegion.Max.Y-cropOffsetY) * sy
						if crX1 < 0 {
							crX1 = 0
						}
						if crY1 < 0 {
							crY1 = 0
						}
						if crX2 > float32(displayFrame.Cols()) {
							crX2 = float32(displayFrame.Cols())
						}
						if crY2 > float32(displayFrame.Rows()) {
							crY2 = float32(displayFrame.Rows())
						}
						if crX2 > crX1 && crY2 > crY1 {
							gocv.Rectangle(&displayFrame,
								image.Rect(int(crX1), int(crY1), int(crX2), int(crY2)),
								color.RGBA{R: 0, G: 128, B: 255, A: 255}, 3)
						}
					}
				}
			}
		}

		if vcam != nil && vcam.IsOpen() {
			if err := vcam.WriteFrame(displayFrame); err != nil {
				log.Printf("Virtual camera write error: %v", err)
			}
		}

		frame.Close()
		if hasCroppedFrame {
			if hasBgProcessedFrame {
				croppedFrame.Close()
			} else {
				displayFrame.Close()
			}
		} else if hasBgProcessedFrame {
			displayFrame.Close()
		}

		frameCount++

		if time.Since(lastMetricsTime) > 3*time.Second {
			elapsed := time.Since(lastMetricsTime).Seconds()
			fmt.Printf("[%s] FPS: %.1f | Frames: %d | Target: %d FPS\n",
				time.Now().Format("15:04:05"), float64(frameCount)/elapsed, frameCount, targetFPS)
			lastMetricsTime = time.Now()
			frameCount = 0
		}
	}
}

func main() {
	configFile := ""
	for i, arg := range os.Args[1:] {
		if arg == "-config" || arg == "--config" {
			if i+1 < len(os.Args)-1 {
				configFile = os.Args[i+2]
			}
			break
		}
		if arg == "-C" {
			if i+1 < len(os.Args)-1 {
				configFile = os.Args[i+2]
			}
			break
		}
		if len(arg) > 2 && arg[:2] == "-C" && arg[2] != '=' {
			configFile = arg[2:]
			break
		}
	}

	configFile = config.ResolvePath(configFile)
	cfg, err := config.LoadFromFile(configFile)
	if err != nil {
		log.Fatalf("Failed to load config file: %v", err)
	}

	cameraIndex := flag.Int("camera", cfg.CameraIndex, "Camera device index")
	flag.IntVar(cameraIndex, "c", cfg.CameraIndex, "Alias for -camera")
	width := flag.Int("width", cfg.Width, "Capture width")
	flag.IntVar(width, "w", cfg.Width, "Alias for -width")
	height := flag.Int("height", cfg.Height, "Capture height")
	flag.IntVar(height, "h", cfg.Height, "Alias for -height")
	fps := flag.Int("fps", cfg.FPS, "Target FPS")
	flag.IntVar(fps, "f", cfg.FPS, "Alias for -fps")
	format := flag.String("format", cfg.Format, "Camera format (FOURCC)")
	flag.StringVar(format, "fmt", cfg.Format, "Alias for -format")
	detectionFPS := flag.Float64("detection-fps", float64(cfg.DetectionFPS), "Target detections per second")
	flag.Float64Var(detectionFPS, "d", float64(cfg.DetectionFPS), "Alias for -detection-fps")
	enableFraming := flag.Bool("framing", true, "Enable auto-framing")
	flag.BoolVar(enableFraming, "z", true, "Alias for -framing")
	smoothingFactor := flag.Float64("smoothing", float64(cfg.SmoothingFactor), "Smoothing factor (0.0-1.0)")
	flag.Float64Var(smoothingFactor, "s", float64(cfg.SmoothingFactor), "Alias for -smoothing")
	zoomMin := flag.Float64("zoom-min", float64(cfg.ZoomMin), "Minimum zoom level")
	flag.Float64Var(zoomMin, "zm", float64(cfg.ZoomMin), "Alias for -zoom-min")
	zoomMax := flag.Float64("zoom-max", float64(cfg.ZoomMax), "Maximum zoom level")
	flag.Float64Var(zoomMax, "zM", float64(cfg.ZoomMax), "Alias for -zoom-max")
	targetFaceSizeMin := flag.Float64("target-face-size-min", float64(cfg.TargetFaceSizeMin), "Target face size min")
	flag.Float64Var(targetFaceSizeMin, "tfsm", float64(cfg.TargetFaceSizeMin), "Alias for -target-face-size-min")
	targetFaceSizeMax := flag.Float64("target-face-size-max", float64(cfg.TargetFaceSizeMax), "Target face size max")
	flag.Float64Var(targetFaceSizeMax, "tfsM", float64(cfg.TargetFaceSizeMax), "Alias for -target-face-size-max")
	zoomSmoothingFactor := flag.Float64("zoom-smoothing", float64(cfg.ZoomSmoothingFactor), "Zoom smoothing factor")
	flag.Float64Var(zoomSmoothingFactor, "zs", float64(cfg.ZoomSmoothingFactor), "Alias for -zoom-smoothing")
	faceBoxPadding := flag.Float64("face-box-padding", float64(cfg.FaceBoxPadding), "Face box padding multiplier")
	flag.Float64Var(faceBoxPadding, "fbp", float64(cfg.FaceBoxPadding), "Alias for -face-box-padding")
	backgroundEnabled := flag.Bool("background", cfg.BackgroundEnabled, "Enable background processing")
	flag.BoolVar(backgroundEnabled, "bg", cfg.BackgroundEnabled, "Alias for -background")
	backgroundMode := flag.String("background-mode", cfg.BackgroundMode, "Background mode: blur or replace")
	flag.StringVar(backgroundMode, "bgm", cfg.BackgroundMode, "Alias for -background-mode")
	backgroundBlurStrength := flag.Int("background-blur", cfg.BackgroundBlurStrength, "Background blur strength")
	flag.IntVar(backgroundBlurStrength, "bgb", cfg.BackgroundBlurStrength, "Alias for -background-blur")
	backgroundImagePath := flag.String("background-image", cfg.BackgroundImagePath, "Background image path")
	flag.StringVar(backgroundImagePath, "bgi", cfg.BackgroundImagePath, "Alias for -background-image")
	backgroundColor := flag.String("background-color", cfg.BackgroundColor, "Background color (hex)")
	flag.StringVar(backgroundColor, "bgc", cfg.BackgroundColor, "Alias for -background-color")
	backgroundFacePadding := flag.Float64("background-face-padding", float64(cfg.BackgroundFacePadding), "Background face padding")
	flag.Float64Var(backgroundFacePadding, "bgfp", float64(cfg.BackgroundFacePadding), "Alias for -background-face-padding")
	logLevel := flag.String("log-level", cfg.LogLevel, "Log level")
	flag.StringVar(logLevel, "l", cfg.LogLevel, "Alias for -log-level")
	modelPath := flag.String("model", cfg.ModelPath, "ONNX model path")
	flag.StringVar(modelPath, "m", cfg.ModelPath, "Alias for -model")
	noDetection := flag.Bool("no-detection", cfg.NoDetection, "Disable face detection")
	flag.BoolVar(noDetection, "nd", cfg.NoDetection, "Alias for -no-detection")
	enableVirtualCam := flag.Bool("virtual-cam", cfg.VirtualCamEnabled, "Enable virtual camera output")
	flag.BoolVar(enableVirtualCam, "v", cfg.VirtualCamEnabled, "Alias for -virtual-cam")
	virtualCamDevice := flag.String("virtual-cam-device", cfg.VirtualCamDevice, "Virtual camera device")
	flag.StringVar(virtualCamDevice, "vd", cfg.VirtualCamDevice, "Alias for -virtual-cam-device")
	virtualCamFormat := flag.String("virtual-cam-format", cfg.VirtualCamFormat, "Virtual camera format")
	flag.StringVar(virtualCamFormat, "vf", cfg.VirtualCamFormat, "Alias for -virtual-cam-format")
	listCameras := flag.Bool("list-cameras", false, "List cameras and exit")
	flag.BoolVar(listCameras, "lc", false, "Alias for -list-cameras")
	listCapabilities := flag.Bool("list-capabilities", false, "List camera capabilities and exit")
	flag.BoolVar(listCapabilities, "lC", false, "Alias for -list-capabilities")
	cpuProfile := flag.String("cpuprofile", "", "Write CPU profile to file")
	flag.StringVar(cpuProfile, "cp", "", "Alias for -cpuprofile")
	_ = flag.String("config", "", "Path to config file")
	flag.String("C", "", "Alias for -config")
	apiEnabled := flag.Bool("api", cfg.APIEnabled, "Enable HTTP API server")
	flag.BoolVar(apiEnabled, "a", cfg.APIEnabled, "Alias for -api")
	apiPort := flag.Int("api-port", cfg.APIPort, "API server port")
	flag.IntVar(apiPort, "ap", cfg.APIPort, "Alias for -api-port")
	startCapture := flag.Bool("start-capture", cfg.StartCapture, "Start capture at launch")
	flag.BoolVar(startCapture, "sc", cfg.StartCapture, "Alias for -start-capture")
	pipelineSubprocess := flag.Bool("pipeline-subprocess", false, "")
	controlPort := flag.Int("control-port", 0, "")

	flag.Parse()

	cfg.CameraIndex = *cameraIndex
	cfg.Width = *width
	cfg.Height = *height
	cfg.FPS = *fps
	cfg.Format = *format
	cfg.DetectionFPS = float32(*detectionFPS)
	cfg.SmoothingFactor = float32(*smoothingFactor)
	cfg.ZoomMin = float32(*zoomMin)
	cfg.ZoomMax = float32(*zoomMax)
	cfg.TargetFaceSizeMin = float32(*targetFaceSizeMin)
	cfg.TargetFaceSizeMax = float32(*targetFaceSizeMax)
	cfg.ZoomSmoothingFactor = float32(*zoomSmoothingFactor)
	cfg.FaceBoxPadding = float32(*faceBoxPadding)
	cfg.BackgroundEnabled = *backgroundEnabled
	cfg.BackgroundMode = *backgroundMode
	cfg.BackgroundBlurStrength = *backgroundBlurStrength
	cfg.BackgroundImagePath = *backgroundImagePath
	cfg.BackgroundColor = *backgroundColor
	cfg.BackgroundFacePadding = float32(*backgroundFacePadding)
	cfg.LogLevel = *logLevel
	cfg.ModelPath = *modelPath
	cfg.VirtualCamEnabled = *enableVirtualCam
	cfg.VirtualCamDevice = *virtualCamDevice
	cfg.VirtualCamFormat = *virtualCamFormat
	cfg.NoDetection = *noDetection
	cfg.APIEnabled = *apiEnabled
	cfg.APIPort = *apiPort
	cfg.StartCapture = *startCapture

	// Subprocess mode: only run the pipeline, then exit.
	// The parent process re-execs this binary with this flag to isolate all
	// heavy libraries (CUDA, cuDNN, ONNX Runtime) in a child process whose
	// memory is fully reclaimed by the OS when the pipeline stops.
	if *pipelineSubprocess {
		logger.Init(cfg.LogLevel)
		ctx, cancel := context.WithCancel(context.Background())
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
		go func() { <-sigChan; cancel() }()
		var af atomic.Pointer[cameraman.Framer]
		if configFile != "" {
			if stop := watchConfig(configFile, cfg, &af); stop != nil {
				defer stop()
			}
		}
		// Camera control server: the parent API forwards /cameras* requests here,
		// since the camera manager lives in this process.
		var control *api.Server
		if *controlPort > 0 {
			control = api.NewServer(*controlPort, nil, nil)
			if err := control.StartCameraControl(); err != nil {
				log.Printf("Warning: camera control server: %v", err)
				control = nil
			} else {
				defer control.Stop(context.Background())
			}
		}
		runPipeline(ctx, cfg, *enableFraming, &af, control)
		return
	}

	if *listCameras {
		fmt.Println("Scanning for available cameras...")
		available := camera.ListAvailableCameras()
		if len(available) == 0 {
			fmt.Println("No cameras found.")
			os.Exit(0)
		}
		fmt.Printf("\nFound %d available camera(s):\n\n", len(available))
		for _, idx := range available {
			devicePath := fmt.Sprintf("/dev/video%d", idx)
			info := ""
			if _, err := os.Stat(devicePath); err == nil {
				info = fmt.Sprintf(" (%s)", devicePath)
			}
			fmt.Printf("  Camera index %d%s\n", idx, info)
		}
		fmt.Printf("\nUsage: ./bin/virtualcam -camera <index>\n")
		os.Exit(0)
	}

	if *listCapabilities {
		fmt.Printf("Querying capabilities for camera %d...\n\n", cfg.CameraIndex)
		capabilities, err := camera.GetCameraCapabilities(cfg.CameraIndex)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(capabilities.ListCapabilities())
		os.Exit(0)
	}

	logger.Init(cfg.LogLevel)
	log.Printf("Virtual Cameraman starting")

	// Resolve model path
	resolvedModelPath := cfg.ModelPath
	if !cfg.NoDetection {
		modelDir := os.Getenv("MODEL_DIR")
		if modelDir == "" {
			modelDir = os.Getenv("ONNXRUNTIME_HOME")
		}
		var resolveErr error
		if cfg.ModelPath == "" {
			if modelDir != "" {
				candidates := []string{
					filepath.Join(modelDir, "model_gpu.onnx"),
					filepath.Join(modelDir, "model.onnx"),
				}
				found := false
				for _, candidate := range candidates {
					if _, err := os.Stat(candidate); err == nil {
						resolvedModelPath = candidate
						found = true
						break
					}
				}
				if !found {
					resolveErr = fmt.Errorf("no default model in MODEL_DIR (%s)", modelDir)
				}
			} else {
				resolveErr = fmt.Errorf("model path empty and MODEL_DIR not set")
			}
		} else if filepath.IsAbs(cfg.ModelPath) {
			if _, err := os.Stat(cfg.ModelPath); err != nil {
				resolveErr = fmt.Errorf("model file not found: %s", cfg.ModelPath)
			}
		} else {
			if modelDir != "" {
				resolved := filepath.Join(modelDir, cfg.ModelPath)
				if _, err := os.Stat(resolved); err == nil {
					resolvedModelPath = resolved
				}
			}
			if resolvedModelPath == cfg.ModelPath {
				if _, err := os.Stat(cfg.ModelPath); err == nil {
					abs, _ := filepath.Abs(cfg.ModelPath)
					resolvedModelPath = abs
				} else {
					resolveErr = fmt.Errorf("could not resolve model path: %s", cfg.ModelPath)
				}
			}
		}
		if resolveErr != nil {
			if cfg.ModelPath != "" {
				log.Printf("Warning: %v", resolveErr)
			} else {
				log.Printf("Info: no model specified (%v)", resolveErr)
			}
			resolvedModelPath = ""
		} else if cfg.ModelPath == "" && resolvedModelPath != "" {
			log.Printf("Using default model: %s", resolvedModelPath)
		}
	}
	cfg.ModelPath = resolvedModelPath

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatalf("Failed to create CPU profile: %v", err)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatalf("Failed to start CPU profile: %v", err)
		}
		defer pprof.StopCPUProfile()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Pipeline start/stop via subprocess.
	// All heavy libraries (CUDA, cuDNN, ONNX Runtime, OpenCV) are loaded only
	// inside the child process. When the child exits the OS reclaims all memory,
	// including pages mapped from shared libraries that dlclose never truly frees.
	var (
		pipelineMu     sync.Mutex
		pipelineCmd    *exec.Cmd
		pipelineDone   chan struct{}
		pipelineActive atomic.Bool
	)

	selfPath, err := os.Executable()
	if err != nil {
		selfPath = os.Args[0]
	}

	var apiServer *api.Server

	startPipeline := func() {
		pipelineMu.Lock()
		defer pipelineMu.Unlock()
		if pipelineActive.Load() {
			return
		}
		// Forward all original args plus --pipeline-subprocess so the child
		// skips the API server and starts capture immediately. The child also
		// watches the config file for hot-reload.
		args := append(os.Args[1:], "--pipeline-subprocess", "--start-capture=true")
		controlPort := 0
		if apiServer != nil {
			controlPort = cfg.APIPort + 1
			args = append(args, fmt.Sprintf("--control-port=%d", controlPort))
		}
		cmd := exec.Command(selfPath, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Printf("Failed to start pipeline subprocess: %v", err)
			return
		}
		done := make(chan struct{})
		pipelineCmd = cmd
		pipelineDone = done
		pipelineActive.Store(true)
		if apiServer != nil {
			apiServer.SetPipelinePort(controlPort)
		}
		go func() {
			defer close(done)
			defer pipelineActive.Store(false)
			if err := cmd.Wait(); err != nil {
				log.Printf("Pipeline subprocess exited: %v", err)
			}
			if apiServer != nil {
				apiServer.SetPipelinePort(0)
			}
		}()
		log.Printf("Pipeline subprocess started (pid %d)", cmd.Process.Pid)
	}

	stopPipeline := func() {
		pipelineMu.Lock()
		if !pipelineActive.Load() {
			pipelineMu.Unlock()
			return
		}
		cmd := pipelineCmd
		done := pipelineDone
		pipelineMu.Unlock()
		log.Printf("Sending SIGTERM to pipeline subprocess (pid %d)...", cmd.Process.Pid)
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			log.Printf("Warning: failed to signal subprocess: %v", err)
		}
		<-done
		log.Println("Pipeline subprocess stopped")
	}

	toggleCapture := func(source string) {
		if pipelineActive.Load() {
			log.Printf("Stopping pipeline (%s)...", source)
			stopPipeline()
		} else {
			log.Printf("Starting pipeline (%s)...", source)
			startPipeline()
		}
	}

	// API server (always starts, even when capture is off)
	if cfg.APIEnabled {
		getState := func() (bool, time.Time) {
			return pipelineActive.Load(), time.Now()
		}
		apiServer = api.NewServer(cfg.APIPort, getState, func() {
			toggleCapture("api")
		})
		if err := apiServer.Start(); err != nil {
			log.Printf("Warning: failed to start API server: %v", err)
			apiServer = nil
		} else {
			defer func() {
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutdownCancel()
				apiServer.Stop(shutdownCtx)
			}()
		}
	} else {
		log.Println("API server disabled (use --api to enable)")
	}

	if cfg.StartCapture {
		log.Println("start_capture=true: starting pipeline immediately")
		startPipeline()
	} else {
		log.Println("start_capture=false: idle. Use API POST /toggle or SIGUSR1 to start.")
	}

	// Signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	pauseSignalChan := make(chan os.Signal, 1)
	signal.Notify(pauseSignalChan, syscall.SIGUSR1)

	for {
		select {
		case <-ctx.Done():
			stopPipeline()
			return
		case <-sigChan:
			log.Println("Shutdown signal received")
			stopPipeline()
			cancel()
			return
		case <-pauseSignalChan:
			toggleCapture("signal")
		}
	}
}
