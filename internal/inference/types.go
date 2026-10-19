package inference

import (
	"fmt"
	"image"
	"runtime"

	"gocv.io/x/gocv"
)

// BoundingBox represents a detected face bounding box
type BoundingBox struct {
	X1         float32
	Y1         float32
	X2         float32
	Y2         float32
	Confidence float32
}

// Rect converts BoundingBox to image.Rectangle
func (b *BoundingBox) Rect() image.Rectangle {
	return image.Rect(int(b.X1), int(b.Y1), int(b.X2), int(b.Y2))
}

// Width returns bounding box width
func (b *BoundingBox) Width() float32 {
	return b.X2 - b.X1
}

// Height returns bounding box height
func (b *BoundingBox) Height() float32 {
	return b.Y2 - b.Y1
}

// Area returns bounding box area
func (b *BoundingBox) Area() float32 {
	return b.Width() * b.Height()
}

// Detector is the interface for face detection models
type Detector interface {
	// DetectFaces runs inference on a frame and returns bounding boxes
	DetectFaces(frame gocv.Mat) ([]BoundingBox, error)

	// GetExecutionProvider returns the provider used (CUDA, CPU, etc.)
	GetExecutionProvider() string

	// GetInferenceTime returns the last inference time in milliseconds
	GetInferenceTime() float32

	// Close releases resources
	Close() error
}

// MetricsCollector tracks inference metrics
type MetricsCollector struct {
	InferenceTime float32
	FrameCount    int64
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		InferenceTime: 0,
		FrameCount:    0,
	}
}

// GetPlatformInfo returns platform and architecture info
func GetPlatformInfo() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}
