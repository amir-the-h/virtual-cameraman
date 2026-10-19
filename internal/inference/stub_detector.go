package inference

import (
	"errors"
	"time"

	"gocv.io/x/gocv"
)

type stubDetector struct {
	provider    string
	lastInferMs float64
}

func (s *stubDetector) DetectFaces(frame gocv.Mat) ([]BoundingBox, error) {
	if frame.Empty() {
		return nil, errors.New("empty frame")
	}
	start := time.Now()
	// No real detection; return empty result quickly
	time.Sleep(5 * time.Millisecond)
	s.lastInferMs = float64(time.Since(start).Milliseconds())
	return []BoundingBox{}, nil
}

func (s *stubDetector) Close() error {
	// Nothing to close for stub
	return nil
}

func (s *stubDetector) GetExecutionProvider() string {
	return s.provider
}

func (s *stubDetector) GetInferenceTime() float64 {
	return s.lastInferMs
}
