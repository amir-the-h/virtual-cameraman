package camera

import "gocv.io/x/gocv"

// Source represents any video source (local webcam, RTSP stream, file, etc.)
type Source interface {
	// Open initializes the source
	Open() error

	// Read returns the next frame
	Read() (gocv.Mat, error)

	// IsOpened returns true if the source is ready
	IsOpened() bool

	// Close releases the source
	Close() error

	// Name returns a human-readable identifier
	Name() string

	// Width returns the frame width
	Width() int

	// Height returns the frame height
	Height() int

	// FPS returns the frame rate
	FPS() int
}
