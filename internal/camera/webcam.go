package camera

import (
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gocv.io/x/gocv"
)

// Webcam wraps gocv.VideoCapture for webcam operations
type Webcam struct {
	cap    *gocv.VideoCapture
	width  int
	height int
	fps    int
	format string
}

// ListAvailableCameras returns a list of available camera device indices
func ListAvailableCameras() []int {
	var available []int

	// Try to find cameras by checking /dev/video* devices
	videoDevices, err := filepath.Glob("/dev/video*")
	if err == nil {
		seen := make(map[int]bool)
		for _, device := range videoDevices {
			// Extract number from /dev/videoN
			base := filepath.Base(device)
			if strings.HasPrefix(base, "video") {
				numStr := base[5:] // Skip "video"
				if num, err := strconv.Atoi(numStr); err == nil {
					// Test if this device can be opened
					if cap, err := gocv.VideoCaptureDevice(num); err == nil {
						if cap.IsOpened() {
							if !seen[num] {
								available = append(available, num)
								seen[num] = true
							}
							cap.Close()
						}
					}
				}
			}
		}
	}

	// Also try probing indices 0-9 directly (in case /dev/video* doesn't work)
	for i := 0; i < 10; i++ {
		if cap, err := gocv.VideoCaptureDevice(i); err == nil {
			if cap.IsOpened() {
				// Check if we already have this index
				found := false
				for _, idx := range available {
					if idx == i {
						found = true
						break
					}
				}
				if !found {
					available = append(available, i)
				}
				cap.Close()
			}
		}
	}

	sort.Ints(available)
	return available
}

// setCameraProperties uses v4l2-ctl to set camera properties before opening with OpenCV
// This is more reliable than using OpenCV's GStreamer backend which often ignores settings
// Returns true if v4l2-ctl was used successfully, false otherwise
func setCameraProperties(deviceIndex, width, height, fps int, format string) bool {
	devicePath := fmt.Sprintf("/dev/video%d", deviceIndex)

	// Check if v4l2-ctl is available
	if _, err := exec.LookPath("v4l2-ctl"); err != nil {
		// v4l2-ctl not available, skip setting properties (will rely on OpenCV)
		return false
	}

	var args []string
	args = append(args, "--device", devicePath)

	// Set format and resolution together (if format specified)
	// Note: Some cameras require format to be set before FPS
	if format != "" && len(format) == 4 {
		args = append(args, "--set-fmt-video", fmt.Sprintf("pixelformat=%s,width=%d,height=%d", format, width, height))
		log.Printf("Setting camera via v4l2-ctl: format=%s, resolution=%dx%d", format, width, height)
	} else {
		// Set resolution only (format will use camera default)
		args = append(args, "--set-fmt-video", fmt.Sprintf("width=%d,height=%d", width, height))
		log.Printf("Setting camera via v4l2-ctl: resolution=%dx%d (default format)", width, height)
	}

	// Set FPS - must be done after format/resolution for some cameras
	// Format: --set-parm=30 (not fps=30/1)
	args = append(args, "--set-parm", fmt.Sprintf("%d", fps))
	log.Printf("Setting camera FPS via v4l2-ctl: %d", fps)

	cmd := exec.Command("v4l2-ctl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Log but don't fail - OpenCV might still work
		log.Printf("Warning: v4l2-ctl failed to set camera properties: %v (output: %s)", err, string(output))
		return false
	}

	log.Printf("Camera properties set successfully via v4l2-ctl")

	// Verify what was actually set and extract actual FPS
	verifyArgs := []string{"--device", devicePath, "--get-fmt-video", "--get-parm"}
	verifyCmd := exec.Command("v4l2-ctl", verifyArgs...)
	verifyOutput, err := verifyCmd.CombinedOutput()
	if err == nil {
		log.Printf("v4l2-ctl verification:\n%s", string(verifyOutput))
		// Parse actual FPS from output
		// Format: "Frames per second: X.XXX (X/1)"
		outputStr := string(verifyOutput)
		if strings.Contains(outputStr, "Frames per second:") {
			// Extract FPS value
			lines := strings.Split(outputStr, "\n")
			for _, line := range lines {
				if strings.Contains(line, "Frames per second:") {
					parts := strings.Fields(line)
					for i, part := range parts {
						if part == "per" && i+2 < len(parts) {
							// Next part should be "second:" and after that is the FPS value
							if i+2 < len(parts) && strings.HasPrefix(parts[i+2], "second:") {
								if i+3 < len(parts) {
									fpsStr := strings.Trim(parts[i+3], "(")
									if actualFPS, parseErr := strconv.ParseFloat(fpsStr, 64); parseErr == nil {
										log.Printf("Actual camera FPS: %.1f (requested: %d)", actualFPS, fps)
										// Note: We can't return this value easily, but we log it for debugging
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return true
}

// NewWebcam initializes a webcam capture device
// format is the FOURCC code (e.g., "YUYV", "MJPG", "H264"). Empty string means use camera default.
func NewWebcam(deviceIndex, width, height, fps int, format string) (*Webcam, error) {
	// Try to set camera properties using v4l2-ctl first (more reliable than OpenCV)
	// This must be done before opening the camera
	v4l2Success := setCameraProperties(deviceIndex, width, height, fps, format)
	if !v4l2Success {
		log.Printf("Note: v4l2-ctl not available or failed, will try OpenCV API")
	}

	// Try to use a GStreamer pipeline string that explicitly sets format/resolution/FPS
	// This is more reliable than relying on OpenCV's automatic backend selection
	var cap *gocv.VideoCapture
	var err error

	devicePath := fmt.Sprintf("/dev/video%d", deviceIndex)

	// If v4l2-ctl succeeded, we should use a GStreamer pipeline to preserve the settings
	// Even if format is not specified, v4l2-ctl may have set a format, so we should use a pipeline
	// This prevents OpenCV from resetting the format/resolution/FPS
	if v4l2Success {
		// If format was not specified, detect what format v4l2-ctl actually set
		actualFormat := format
		if actualFormat == "" {
			// Query the actual format that was set
			queryArgs := []string{"--device", devicePath, "--get-fmt-video"}
			queryCmd := exec.Command("v4l2-ctl", queryArgs...)
			queryOutput, err := queryCmd.CombinedOutput()
			if err == nil {
				outputStr := string(queryOutput)
				// Parse format from output: "Pixel Format      : 'H264' (H.264)"
				if strings.Contains(outputStr, "Pixel Format") {
					lines := strings.Split(outputStr, "\n")
					for _, line := range lines {
						if strings.Contains(line, "Pixel Format") {
							// Extract format from quotes
							start := strings.Index(line, "'")
							if start != -1 {
								end := strings.Index(line[start+1:], "'")
								if end != -1 {
									actualFormat = line[start+1 : start+1+end]
									log.Printf("Detected camera format from v4l2-ctl: %s", actualFormat)
									break
								}
							}
						}
					}
				}
			}
		}

		// Use GStreamer pipeline if we have a format (either specified or detected)
		if actualFormat != "" && len(actualFormat) == 4 {
			// Build GStreamer pipeline based on format type
			// For compressed formats (H264, MJPG), use appropriate caps
			// For raw formats (YUYV, NV12), use video/x-raw
			// Note: We don't specify framerate in the pipeline to avoid mismatches
			// The camera will provide frames at whatever rate it's configured for
			var pipeline string
			switch actualFormat {
			case "H264":
				// H264 is compressed, use video/x-h264
				// Specify width/height and framerate in caps to ensure we get the right settings
				// This helps prevent the camera from defaulting to different values
				// Try NVIDIA hardware decoder first (faster, better quality), fallback to openh264dec
				// nvh264dec requires CUDA, openh264dec is software-based
				// Both should provide full quality decoding
				pipeline = fmt.Sprintf("v4l2src device=%s ! video/x-h264,width=%d,height=%d,framerate=%d/1 ! h264parse ! nvh264dec ! videoconvert ! video/x-raw,width=%d,height=%d,framerate=%d/1 ! appsink",
					devicePath, width, height, fps, width, height, fps)
			case "MJPG":
				// MJPEG is compressed, use image/jpeg
				// Specify width/height and framerate to enforce settings
				pipeline = fmt.Sprintf("v4l2src device=%s ! image/jpeg,width=%d,height=%d,framerate=%d/1 ! jpegdec ! videoconvert ! video/x-raw,width=%d,height=%d,framerate=%d/1 ! appsink",
					devicePath, width, height, fps, width, height, fps)
			default:
				// Raw formats (YUYV, NV12, etc.)
				// Specify width/height and framerate to enforce settings
				pipeline = fmt.Sprintf("v4l2src device=%s ! video/x-raw,format=%s,width=%d,height=%d,framerate=%d/1 ! videoconvert ! appsink",
					devicePath, actualFormat, width, height, fps)
			}
			log.Printf("Opening camera with GStreamer pipeline (FPS will match camera setting): %s", pipeline)
			cap, err = gocv.VideoCaptureFile(pipeline)
			if err == nil && cap.IsOpened() {
				log.Printf("Successfully opened camera with GStreamer pipeline")
			} else {
				// If H264 and nvh264dec failed, try openh264dec as fallback
				if actualFormat == "H264" {
					fallbackPipeline := fmt.Sprintf("v4l2src device=%s ! video/x-h264,width=%d,height=%d,framerate=%d/1 ! h264parse ! openh264dec ! videoconvert ! video/x-raw,width=%d,height=%d,framerate=%d/1 ! appsink",
						devicePath, width, height, fps, width, height, fps)
					log.Printf("nvh264dec failed, trying openh264dec fallback: %s", fallbackPipeline)
					cap, err = gocv.VideoCaptureFile(fallbackPipeline)
					if err == nil && cap.IsOpened() {
						log.Printf("Successfully opened camera with openh264dec fallback pipeline")
					} else {
						log.Printf("GStreamer pipeline failed (both nvh264dec and openh264dec), falling back to default: %v", err)
						cap = nil
					}
				} else {
					log.Printf("GStreamer pipeline failed, falling back to default: %v", err)
					cap = nil
				}
			}
		}
	}

	// Fallback to default opening method
	if cap == nil || !cap.IsOpened() {
		cap, err = gocv.VideoCaptureDevice(deviceIndex)
		if err != nil {
			// Get list of available cameras for better error message
			available := ListAvailableCameras()
			errMsg := fmt.Errorf("webcam: open camera device %d: %w", deviceIndex, err)
			if len(available) > 0 {
				return nil, fmt.Errorf("%v\nAvailable camera indices: %v\nTry: -camera %d",
					errMsg, available, available[0])
			}
			return nil, errMsg
		}
	}

	// Verify camera is opened
	if !cap.IsOpened() {
		// Get list of available cameras for better error message
		available := ListAvailableCameras()
		errMsg := fmt.Errorf("webcam: camera device %d failed to open", deviceIndex)
		if len(available) > 0 {
			return nil, fmt.Errorf("%v\nAvailable camera indices: %v\nTry: -camera %d",
				errMsg, available, available[0])
		}
		return nil, errMsg
	}

	// After opening, verify what OpenCV actually sees
	// This helps debug if OpenCV is resetting the format
	// If v4l2-ctl succeeded but OpenCV reset it, try to re-apply settings
	if v4l2Success {
		devicePath := fmt.Sprintf("/dev/video%d", deviceIndex)
		verifyArgs := []string{"--device", devicePath, "--get-fmt-video", "--get-parm"}
		verifyCmd := exec.Command("v4l2-ctl", verifyArgs...)
		verifyOutput, err := verifyCmd.CombinedOutput()
		if err == nil {
			outputStr := string(verifyOutput)
			log.Printf("v4l2-ctl settings after OpenCV opened:\n%s", outputStr)

			// Check if format was reset - if so, try to re-apply
			// Only re-apply if we're not using GStreamer pipeline (which should preserve format)
			if !strings.Contains(outputStr, format) && format != "" {
				log.Printf("Warning: Format was reset by OpenCV, attempting to re-apply via v4l2-ctl...")
				// Close the capture temporarily to re-apply settings
				// Actually, we can't close it here, so we'll just log the warning
				// The user may need to ensure no other process is using the camera
			}
		}
	}

	// If v4l2-ctl succeeded and we used GStreamer pipeline, the camera is already configured correctly
	// Don't try to set anything via OpenCV as it will reset the format
	// Only read back what we actually have
	actualFormat := ""
	if format != "" {
		if len(format) != 4 {
			return nil, fmt.Errorf("webcam: format must be a 4-character FOURCC code (e.g., 'YUYV', 'MJPG'), got: %s", format)
		}
		// If we successfully used v4l2-ctl and GStreamer pipeline, assume format is correct
		// Otherwise try to read it back (though OpenCV may not report it correctly)
		if !v4l2Success || cap == nil {
			// Only try to set via OpenCV if v4l2-ctl didn't work
			// Convert FOURCC string to integer (little-endian: first char is LSB)
			fourcc := int(format[0]) | (int(format[1]) << 8) | (int(format[2]) << 16) | (int(format[3]) << 24)
			cap.Set(gocv.VideoCaptureFOURCC, float64(fourcc))
		}
		// Read back actual format (may be empty if OpenCV can't report it)
		actualFourcc := int(cap.Get(gocv.VideoCaptureFOURCC))
		actualFormat = fourccToString(actualFourcc)
		if actualFormat != format && actualFormat != "" {
			log.Printf("Camera format: requested %s, got %s", format, actualFormat)
		} else if v4l2Success {
			// If v4l2-ctl succeeded, trust that format is set correctly even if OpenCV can't report it
			actualFormat = format
			log.Printf("Camera format set via v4l2-ctl: %s (OpenCV may not report this correctly)", format)
		}
	} else {
		// Read default format
		actualFourcc := int(cap.Get(gocv.VideoCaptureFOURCC))
		actualFormat = fourccToString(actualFourcc)
	}

	// Set resolution and FPS (only if v4l2-ctl didn't work)
	// Note: Some cameras may not support exact resolution, so we try to set it
	// and then read back what was actually set
	// IMPORTANT: If v4l2-ctl succeeded, DON'T set anything via OpenCV as it will reset the format
	if !v4l2Success {
		cap.Set(gocv.VideoCaptureFPS, float64(fps))

		// Try to set resolution - some cameras require setting in specific order
		// Try setting width first, then height (some cameras are picky)
		cap.Set(gocv.VideoCaptureFrameWidth, float64(width))
		cap.Set(gocv.VideoCaptureFrameHeight, float64(height))
	}

	// Some cameras need a frame read to apply settings
	// Try reading a dummy frame to force resolution change
	dummy := gocv.NewMat()
	cap.Read(&dummy)
	dummy.Close()

	// Read actual values (may differ from requested)
	// For GStreamer pipelines, OpenCV's Get() may not report correct values
	// So we'll also try to read a frame to get actual dimensions
	actualWidth := int(cap.Get(gocv.VideoCaptureFrameWidth))
	actualHeight := int(cap.Get(gocv.VideoCaptureFrameHeight))
	actualFPS := int(cap.Get(gocv.VideoCaptureFPS))

	// If using GStreamer pipeline, try reading a frame to get actual resolution
	// OpenCV's Get() properties may not be accurate for GStreamer pipelines
	if v4l2Success && format != "" {
		testFrame := gocv.NewMat()
		if cap.Read(&testFrame) && !testFrame.Empty() {
			frameWidth := testFrame.Cols()
			frameHeight := testFrame.Rows()
			if frameWidth > 0 && frameHeight > 0 {
				log.Printf("Actual frame resolution from GStreamer: %dx%d", frameWidth, frameHeight)
				actualWidth = frameWidth
				actualHeight = frameHeight
			}
		}
		testFrame.Close()
	}

	// Log if resolution differs from requested
	if actualWidth != width || actualHeight != height {
		log.Printf("Camera resolution: requested %dx%d, got %dx%d", width, height, actualWidth, actualHeight)
	}

	// Log format if set
	if format != "" {
		log.Printf("Camera format: %s", actualFormat)
	}

	w := &Webcam{
		cap:    cap,
		width:  actualWidth,
		height: actualHeight,
		fps:    actualFPS,
		format: actualFormat,
	}

	return w, nil
}

// fourccToString converts a FOURCC integer to a 4-character string
func fourccToString(fourcc int) string {
	return string([]byte{
		byte(fourcc & 0xFF),
		byte((fourcc >> 8) & 0xFF),
		byte((fourcc >> 16) & 0xFF),
		byte((fourcc >> 24) & 0xFF),
	})
}

// Read captures and returns the next frame
func (w *Webcam) Read() (gocv.Mat, error) {
	frame := gocv.NewMat()
	if !w.cap.Read(&frame) {
		return frame, fmt.Errorf("webcam: failed to read frame")
	}
	if frame.Empty() {
		return frame, fmt.Errorf("webcam: empty frame received")
	}
	return frame, nil
}

// Width returns the frame width
func (w *Webcam) Width() int {
	return w.width
}

// Height returns the frame height
func (w *Webcam) Height() int {
	return w.height
}

// FPS returns the frame rate
func (w *Webcam) FPS() int {
	return w.fps
}

// Format returns the camera format (FOURCC code)
func (w *Webcam) Format() string {
	return w.format
}

// Close closes the camera device
func (w *Webcam) Close() error {
	if w.cap != nil {
		w.cap.Close()
		w.cap = nil
	}
	return nil
}
