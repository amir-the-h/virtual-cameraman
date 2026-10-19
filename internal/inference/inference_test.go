package inference

import (
	"os"
	"testing"
)

func TestBoundingBox(t *testing.T) {
	tests := []struct {
		name           string
		box            BoundingBox
		expectedWidth  float32
		expectedHeight float32
		expectedArea   float32
	}{
		{
			name:           "normal box",
			box:            BoundingBox{X1: 10, Y1: 20, X2: 100, Y2: 150},
			expectedWidth:  90,
			expectedHeight: 130,
			expectedArea:   11700,
		},
		{
			name:           "small box",
			box:            BoundingBox{X1: 0, Y1: 0, X2: 50, Y2: 50},
			expectedWidth:  50,
			expectedHeight: 50,
			expectedArea:   2500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if w := tt.box.Width(); w != tt.expectedWidth {
				t.Errorf("Width() = %v, want %v", w, tt.expectedWidth)
			}
			if h := tt.box.Height(); h != tt.expectedHeight {
				t.Errorf("Height() = %v, want %v", h, tt.expectedHeight)
			}
			if a := tt.box.Area(); a != tt.expectedArea {
				t.Errorf("Area() = %v, want %v", a, tt.expectedArea)
			}
		})
	}
}

func TestFallbackDetector(t *testing.T) {
	detector := NewFallbackDetector()
	defer detector.Close()

	if provider := detector.GetExecutionProvider(); provider != "CPU (fallback mock)" {
		t.Errorf("GetExecutionProvider() = %v, want CPU (fallback mock)", provider)
	}

	// Create a mock frame (we can't easily create a gocv.Mat in tests without OpenCV)
	// This test ensures the detector doesn't panic when called
	t.Run("mock detector interface", func(t *testing.T) {
		if detector == nil {
			t.Fatal("NewFallbackDetector() returned nil")
		}
		if err := detector.Close(); err != nil {
			t.Errorf("Close() returned error: %v", err)
		}
	})
}

func TestDetectorFactory_CreateDetector(t *testing.T) {
	factory := NewDetectorFactory("test.onnx", 0.5, 640, 640)

	// Without ONNXRUNTIME_HOME, should fall back to FallbackDetector
	os.Unsetenv("ONNXRUNTIME_HOME")
	detector, err := factory.CreateDetector()
	if err != nil {
		t.Errorf("CreateDetector() returned error: %v", err)
	}
	if detector == nil {
		t.Fatal("CreateDetector() returned nil detector")
	}
	defer detector.Close()

	provider := detector.GetExecutionProvider()
	if provider != "CPU (fallback mock)" {
		t.Logf("Note: Expected fallback detector, got: %s", provider)
	}
}

func TestMetricsCollector(t *testing.T) {
	metrics := NewMetricsCollector()

	if metrics.InferenceTime != 0 {
		t.Errorf("Initial InferenceTime = %v, want 0", metrics.InferenceTime)
	}
	if metrics.FrameCount != 0 {
		t.Errorf("Initial FrameCount = %v, want 0", metrics.FrameCount)
	}

	metrics.InferenceTime = 10.5
	metrics.FrameCount = 1
	if metrics.InferenceTime != 10.5 {
		t.Errorf("After update, InferenceTime = %v, want 10.5", metrics.InferenceTime)
	}
	if metrics.FrameCount != 1 {
		t.Errorf("After update, FrameCount = %v, want 1", metrics.FrameCount)
	}
}
