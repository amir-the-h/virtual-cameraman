//go:build cuda

package cuda

import (
	"fmt"
	"image"

	"gocv.io/x/gocv"
	"gocv.io/x/gocv/cuda"
)

// Processor handles GPU-accelerated image processing operations
type Processor struct {
	enabled     bool
	initialized bool
}

// NewProcessor creates a new CUDA processor
// Returns nil if CUDA is not available (graceful fallback)
func NewProcessor() (*Processor, error) {
	// Check if CUDA is available
	deviceCount := cuda.GetCudaEnabledDeviceCount()
	if deviceCount == 0 {
		return nil, fmt.Errorf("cuda: no CUDA devices available")
	}

	proc := &Processor{
		enabled:     true,
		initialized: true,
	}

	return proc, nil
}

// IsEnabled returns true if CUDA is available and enabled
func (p *Processor) IsEnabled() bool {
	return p != nil && p.enabled && p.initialized
}

// Resize resizes an image on GPU
func (p *Processor) Resize(src gocv.Mat, dst *gocv.Mat, size image.Point) error {
	if !p.IsEnabled() {
		return fmt.Errorf("cuda: processor not initialized")
	}

	// Upload to GPU
	gpuSrc := cuda.NewGpuMat()
	defer gpuSrc.Close()
	gpuSrc.Upload(src)

	// Resize on GPU
	gpuDst := cuda.NewGpuMat()
	defer gpuDst.Close()
	err := cuda.Resize(gpuSrc, &gpuDst, size, 0, 0, cuda.InterpolationLinear)
	if err != nil {
		return fmt.Errorf("cuda resize: %w", err)
	}

	// Download from GPU
	gpuDst.Download(dst)
	return nil
}

// CvtColor converts color space on GPU
func (p *Processor) CvtColor(src gocv.Mat, dst *gocv.Mat, code gocv.ColorConversionCode) error {
	if !p.IsEnabled() {
		return fmt.Errorf("cuda: processor not initialized")
	}

	// Upload to GPU
	gpuSrc := cuda.NewGpuMat()
	defer gpuSrc.Close()
	gpuSrc.Upload(src)

	// Convert color on GPU
	gpuDst := cuda.NewGpuMat()
	defer gpuDst.Close()
	err := cuda.CvtColor(gpuSrc, &gpuDst, code)
	if err != nil {
		return fmt.Errorf("cuda cvtColor: %w", err)
	}

	// Download from GPU
	gpuDst.Download(dst)
	return nil
}

// Close releases CUDA resources
func (p *Processor) Close() error {
	if p == nil || !p.initialized {
		return nil
	}

	p.initialized = false
	return nil
}
