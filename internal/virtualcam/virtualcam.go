package virtualcam

import (
	"gocv.io/x/gocv"
)

// VirtualCamera is the interface for virtual camera output
type VirtualCamera interface {
	// WriteFrame writes a frame to the virtual camera device
	// The frame should be in the format expected by the virtual camera
	WriteFrame(frame gocv.Mat) error

	// Close closes the virtual camera device
	Close() error

	// IsOpen returns whether the virtual camera is open and ready
	IsOpen() bool

	// GetDevicePath returns the device path (e.g., "/dev/video2")
	GetDevicePath() string
}
