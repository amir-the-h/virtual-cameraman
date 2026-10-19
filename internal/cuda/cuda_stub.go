//go:build !cuda

package cuda

import (
	"fmt"
	"image"

	"gocv.io/x/gocv"
)

// Processor is a no-op stub when compiled without the cuda build tag.
type Processor struct{}

// NewProcessor always returns an error when CUDA support is not compiled in.
func NewProcessor() (*Processor, error) {
	return nil, fmt.Errorf("cuda: not available (built without cuda tag)")
}

// IsEnabled always returns false for the stub.
func (p *Processor) IsEnabled() bool { return false }

// Resize is a no-op stub.
func (p *Processor) Resize(_ gocv.Mat, _ *gocv.Mat, _ image.Point) error {
	return fmt.Errorf("cuda: not available (built without cuda tag)")
}

// CvtColor is a no-op stub.
func (p *Processor) CvtColor(_ gocv.Mat, _ *gocv.Mat, _ gocv.ColorConversionCode) error {
	return fmt.Errorf("cuda: not available (built without cuda tag)")
}

// Close is a no-op stub.
func (p *Processor) Close() error { return nil }
