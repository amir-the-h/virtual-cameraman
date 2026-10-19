package inference

import (
	"testing"

	"gocv.io/x/gocv"
)

func TestStubDetectorDetectsNothingOnBlankFrame(t *testing.T) {
	d := &stubDetector{provider: "stub"}
	mat := gocv.NewMatWithSize(480, 640, gocv.MatTypeCV8UC3)
	defer mat.Close()

	boxes, err := d.DetectFaces(mat)
	if err != nil {
		t.Fatalf("expected no error for blank frame, got: %v", err)
	}
	if len(boxes) != 0 {
		t.Fatalf("expected zero detections from stub, got %d", len(boxes))
	}
}
