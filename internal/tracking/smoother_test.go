package tracking

import (
	"testing"

	"github.com/amir-the-h/virtual-cameraman/internal/inference"
)

func TestNewSmoother(t *testing.T) {
	tests := []struct {
		name      string
		alpha     float32
		wantAlpha float32
	}{
		{
			name:      "normal alpha",
			alpha:     0.2,
			wantAlpha: 0.2,
		},
		{
			name:      "negative alpha clamped",
			alpha:     -0.5,
			wantAlpha: 0.0,
		},
		{
			name:      "alpha > 1 clamped",
			alpha:     1.5,
			wantAlpha: 1.0,
		},
		{
			name:      "zero alpha",
			alpha:     0.0,
			wantAlpha: 0.0,
		},
		{
			name:      "one alpha",
			alpha:     1.0,
			wantAlpha: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSmoother(tt.alpha)
			if s.alpha != tt.wantAlpha {
				t.Errorf("NewSmoother(%v).alpha = %v, want %v", tt.alpha, s.alpha, tt.wantAlpha)
			}
			if s.initialized {
				t.Error("NewSmoother() should not be initialized")
			}
		})
	}
}

func TestSmoother_Update_FirstFrame(t *testing.T) {
	s := NewSmoother(0.2)
	box := inference.BoundingBox{
		X1:         100,
		Y1:         200,
		X2:         300,
		Y2:         400,
		Confidence: 0.9,
	}

	result := s.Update(box)

	// First frame should be used as-is
	if result.X1 != box.X1 {
		t.Errorf("First frame X1 = %v, want %v", result.X1, box.X1)
	}
	if result.Y1 != box.Y1 {
		t.Errorf("First frame Y1 = %v, want %v", result.Y1, box.Y1)
	}
	if result.X2 != box.X2 {
		t.Errorf("First frame X2 = %v, want %v", result.X2, box.X2)
	}
	if result.Y2 != box.Y2 {
		t.Errorf("First frame Y2 = %v, want %v", result.Y2, box.Y2)
	}
	if result.Confidence != box.Confidence {
		t.Errorf("First frame Confidence = %v, want %v", result.Confidence, box.Confidence)
	}
	if !s.initialized {
		t.Error("Smoother should be initialized after first update")
	}
}

func TestSmoother_Update_Smoothing(t *testing.T) {
	s := NewSmoother(0.2) // alpha = 0.2

	// First frame
	box1 := inference.BoundingBox{X1: 100, Y1: 100, X2: 200, Y2: 200, Confidence: 0.9}
	result1 := s.Update(box1)
	if result1.X1 != 100 {
		t.Errorf("First frame X1 = %v, want 100", result1.X1)
	}

	// Second frame - should be smoothed
	box2 := inference.BoundingBox{X1: 200, Y1: 200, X2: 300, Y2: 300, Confidence: 0.95}
	result2 := s.Update(box2)

	// EMA: smoothed = 0.2 * new + 0.8 * old
	expectedX1 := float32(0.2*200 + 0.8*100) // = 40 + 80 = 120
	if result2.X1 != expectedX1 {
		t.Errorf("Smoothed X1 = %v, want %v", result2.X1, expectedX1)
	}

	// Third frame - should continue smoothing
	box3 := inference.BoundingBox{X1: 300, Y1: 300, X2: 400, Y2: 400, Confidence: 1.0}
	result3 := s.Update(box3)

	// EMA from previous smoothed value
	expectedX1_3 := float32(0.2*300 + 0.8*float64(expectedX1)) // = 60 + 0.8*120 = 60 + 96 = 156
	if result3.X1 != expectedX1_3 {
		t.Errorf("Smoothed X1 (3rd frame) = %v, want %v", result3.X1, expectedX1_3)
	}
}

func TestSmoother_Update_NoSmoothing(t *testing.T) {
	s := NewSmoother(1.0) // alpha = 1.0 means no smoothing

	// First frame
	box1 := inference.BoundingBox{X1: 100, Y1: 100, X2: 200, Y2: 200, Confidence: 0.9}
	s.Update(box1)

	// Second frame - should use new value directly (no smoothing)
	box2 := inference.BoundingBox{X1: 200, Y1: 200, X2: 300, Y2: 300, Confidence: 0.95}
	result2 := s.Update(box2)

	// With alpha = 1.0, should use new value directly
	if result2.X1 != 200 {
		t.Errorf("No smoothing X1 = %v, want 200", result2.X1)
	}
	if result2.Y1 != 200 {
		t.Errorf("No smoothing Y1 = %v, want 200", result2.Y1)
	}
}

func TestSmoother_Reset(t *testing.T) {
	s := NewSmoother(0.2)
	box := inference.BoundingBox{X1: 100, Y1: 100, X2: 200, Y2: 200, Confidence: 0.9}
	s.Update(box)

	if !s.initialized {
		t.Error("Smoother should be initialized after update")
	}

	s.Reset()

	if s.initialized {
		t.Error("Smoother should not be initialized after reset")
	}
	if s.currentBox != nil {
		t.Error("Smoother currentBox should be nil after reset")
	}
}

func TestSmoother_GetCurrent(t *testing.T) {
	s := NewSmoother(0.2)

	// Before initialization
	if s.GetCurrent() != nil {
		t.Error("GetCurrent() should return nil before initialization")
	}

	// After first update
	box := inference.BoundingBox{X1: 100, Y1: 100, X2: 200, Y2: 200, Confidence: 0.9}
	s.Update(box)

	current := s.GetCurrent()
	if current == nil {
		t.Fatal("GetCurrent() should not return nil after update")
	}
	if current.X1 != 100 {
		t.Errorf("GetCurrent().X1 = %v, want 100", current.X1)
	}

	// After reset
	s.Reset()
	if s.GetCurrent() != nil {
		t.Error("GetCurrent() should return nil after reset")
	}
}
