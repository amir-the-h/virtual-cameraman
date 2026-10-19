package inference

import (
	"image"
	"os"
	"testing"

	"gocv.io/x/gocv"
)

// TestOnnxDetector_RealModel checks that a model loads and runs end to end.
// Opt-in, since it needs ONNX Runtime and a model file. Optionally also checks
// that a face is found in a photo:
//
//	VIRTUALCAM_TEST_MODEL=/path/to/yolov11n-face.onnx \
//	VIRTUALCAM_TEST_FACE_IMAGE=/path/to/portrait.jpg \
//	go test ./internal/inference -run RealModel -v
func TestOnnxDetector_RealModel(t *testing.T) {
	modelPath := os.Getenv("VIRTUALCAM_TEST_MODEL")
	if modelPath == "" {
		t.Skip("VIRTUALCAM_TEST_MODEL not set")
	}

	det, err := NewOnnxDetector(modelPath, 0.5, 640, 640)
	if err != nil {
		t.Fatalf("NewOnnxDetector() error: %v", err)
	}
	defer det.Close()

	frame := gocv.NewMatWithSize(720, 1280, gocv.MatTypeCV8UC3)
	defer frame.Close()

	boxes, err := det.DetectFaces(frame)
	if err != nil {
		t.Fatalf("DetectFaces() error: %v", err)
	}
	if len(boxes) != 0 {
		t.Errorf("DetectFaces() on a blank frame = %d boxes, want 0", len(boxes))
	}
	t.Logf("provider=%s inference=%.2fms", det.GetExecutionProvider(), det.GetInferenceTime())

	imagePath := os.Getenv("VIRTUALCAM_TEST_FACE_IMAGE")
	if imagePath == "" {
		return
	}
	img := gocv.IMRead(imagePath, gocv.IMReadColor)
	if img.Empty() {
		t.Fatalf("could not read %s", imagePath)
	}
	defer img.Close()

	faces, err := det.DetectFaces(img)
	if err != nil {
		t.Fatalf("DetectFaces() error: %v", err)
	}
	if len(faces) == 0 {
		t.Fatal("DetectFaces() found no face in the test image")
	}
	bounds := image.Rect(0, 0, img.Cols(), img.Rows())
	for _, f := range faces {
		if !f.Rect().In(bounds) || f.Area() <= 0 {
			t.Errorf("face box %+v is outside the %v image", f, bounds)
		}
	}
	t.Logf("found %d face(s), first at %v (conf %.2f)", len(faces), faces[0].Rect(), faces[0].Confidence)
}
