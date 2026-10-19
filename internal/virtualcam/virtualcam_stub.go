//go:build !linux
// +build !linux

package virtualcam

import (
	"fmt"
	"gocv.io/x/gocv"
)

// StubVirtualCamera is a stub implementation for non-Linux platforms
type StubVirtualCamera struct {
	devicePath string
}

// NewLinuxVirtualCamera is not available on non-Linux platforms
func NewLinuxVirtualCamera(devicePath string, width, height int, pixelFormat string) (VirtualCamera, error) {
	return nil, fmt.Errorf("virtual camera is only supported on Linux (v4l2loopback). Use OBS Virtual Camera or similar on other platforms")
}

// FindAvailableVirtualCamera is not available on non-Linux platforms
func FindAvailableVirtualCamera() (string, error) {
	return "", fmt.Errorf("virtual camera is only supported on Linux (v4l2loopback)")
}

// WriteFrame is a no-op for stub
func (s *StubVirtualCamera) WriteFrame(frame gocv.Mat) error {
	return fmt.Errorf("virtual camera not supported on this platform")
}

// Close is a no-op for stub
func (s *StubVirtualCamera) Close() error {
	return nil
}

// IsOpen returns false for stub
func (s *StubVirtualCamera) IsOpen() bool {
	return false
}

// GetDevicePath returns the device path
func (s *StubVirtualCamera) GetDevicePath() string {
	return s.devicePath
}
