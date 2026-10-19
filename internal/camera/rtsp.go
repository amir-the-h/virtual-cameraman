package camera

import (
	"fmt"
	"log"
	"net/url"

	"gocv.io/x/gocv"
)

// RTSPSource captures from an RTSP stream
type RTSPSource struct {
	url     string
	safeURL string // url with any password masked, for logs and the API
	width   int
	height  int
	fps     int
	capture *gocv.VideoCapture
}

// NewRTSPSource creates a new RTSP stream source
func NewRTSPSource(url string) *RTSPSource {
	return &RTSPSource{
		url:     url,
		safeURL: RedactURL(url),
	}
}

// RedactURL masks the password in a stream URL (rtsp://user:xxxxx@host/...)
// so credentials don't end up in logs or API responses.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid URL>"
	}
	return u.Redacted()
}

func (r *RTSPSource) Open() error {
	var err error
	log.Printf("Opening RTSP stream: %s", r.safeURL)

	// Try to open RTSP stream with GStreamer pipeline for better reliability
	// This pipeline handles RTSP transport and buffering better than default OpenCV
	pipeline := fmt.Sprintf("rtspsrc location=%s latency=100 ! decodebin ! videoconvert ! appsink", r.url)
	r.capture, err = gocv.VideoCaptureFile(pipeline)

	if err != nil || !r.capture.IsOpened() {
		// Fallback to direct RTSP URL
		log.Printf("GStreamer pipeline failed, trying direct RTSP: %v", err)
		r.capture, err = gocv.OpenVideoCapture(r.url)
		if err != nil {
			return fmt.Errorf("open RTSP stream %s: %w", r.safeURL, err)
		}
	}

	if !r.capture.IsOpened() {
		return fmt.Errorf("RTSP stream %s not opened", r.safeURL)
	}

	// Read a test frame to get actual dimensions
	testFrame := gocv.NewMat()
	defer testFrame.Close()

	if r.capture.Read(&testFrame) && !testFrame.Empty() {
		r.width = testFrame.Cols()
		r.height = testFrame.Rows()
		log.Printf("RTSP stream resolution: %dx%d", r.width, r.height)
	} else {
		// Fallback to getting properties from capture
		r.width = int(r.capture.Get(gocv.VideoCaptureFrameWidth))
		r.height = int(r.capture.Get(gocv.VideoCaptureFrameHeight))
	}

	r.fps = int(r.capture.Get(gocv.VideoCaptureFPS))
	if r.fps == 0 {
		r.fps = 30 // Default assumption for RTSP streams
	}

	log.Printf("RTSP stream opened successfully: %s (%dx%d @ %d fps)", r.safeURL, r.width, r.height, r.fps)
	return nil
}

func (r *RTSPSource) Read() (gocv.Mat, error) {
	if r.capture == nil {
		return gocv.NewMat(), fmt.Errorf("RTSP stream not opened")
	}

	frame := gocv.NewMat()
	if !r.capture.Read(&frame) {
		return frame, fmt.Errorf("failed to read RTSP frame")
	}

	if frame.Empty() {
		return frame, fmt.Errorf("empty RTSP frame received")
	}

	return frame, nil
}

func (r *RTSPSource) IsOpened() bool {
	return r.capture != nil && r.capture.IsOpened()
}

func (r *RTSPSource) Close() error {
	if r.capture != nil {
		log.Printf("Closing RTSP stream: %s", r.safeURL)
		err := r.capture.Close()
		r.capture = nil
		return err
	}
	return nil
}

func (r *RTSPSource) Name() string {
	return fmt.Sprintf("RTSP: %s", r.safeURL)
}

func (r *RTSPSource) Width() int {
	return r.width
}

func (r *RTSPSource) Height() int {
	return r.height
}

func (r *RTSPSource) FPS() int {
	return r.fps
}
