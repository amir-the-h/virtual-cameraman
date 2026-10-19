package tracking

import (
	"github.com/amir-the-h/virtual-cameraman/internal/inference"
)

// Smoother applies exponential moving average (EMA) smoothing to bounding boxes
type Smoother struct {
	alpha       float32 // Smoothing factor (0.0 = no smoothing, 1.0 = no change)
	currentBox  *inference.BoundingBox
	initialized bool
}

// NewSmoother creates a new smoother with the given alpha value
// alpha: smoothing factor (typically 0.1-0.3, lower = more smoothing)
func NewSmoother(alpha float32) *Smoother {
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}
	return &Smoother{
		alpha:       alpha,
		initialized: false,
	}
}

// Update applies EMA smoothing to a new bounding box
// Returns the smoothed bounding box
func (s *Smoother) Update(box inference.BoundingBox) inference.BoundingBox {
	if !s.initialized {
		// First frame: use as-is
		s.currentBox = &inference.BoundingBox{
			X1:         box.X1,
			Y1:         box.Y1,
			X2:         box.X2,
			Y2:         box.Y2,
			Confidence: box.Confidence,
		}
		s.initialized = true
		return *s.currentBox
	}

	// Apply EMA: smoothed = alpha * new + (1 - alpha) * old
	s.currentBox.X1 = s.alpha*box.X1 + (1-s.alpha)*s.currentBox.X1
	s.currentBox.Y1 = s.alpha*box.Y1 + (1-s.alpha)*s.currentBox.Y1
	s.currentBox.X2 = s.alpha*box.X2 + (1-s.alpha)*s.currentBox.X2
	s.currentBox.Y2 = s.alpha*box.Y2 + (1-s.alpha)*s.currentBox.Y2
	s.currentBox.Confidence = s.alpha*box.Confidence + (1-s.alpha)*s.currentBox.Confidence

	return *s.currentBox
}

// Reset clears the smoother state
func (s *Smoother) Reset() {
	s.initialized = false
	s.currentBox = nil
}

// SetAlpha updates the smoothing factor (for hot-reload)
func (s *Smoother) SetAlpha(alpha float32) {
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}
	s.alpha = alpha
}

// GetCurrent returns the current smoothed bounding box
func (s *Smoother) GetCurrent() *inference.BoundingBox {
	if !s.initialized {
		return nil
	}
	return s.currentBox
}
