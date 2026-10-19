package cameraman

import (
	"image"
	"testing"

	"github.com/amir-the-h/virtual-cameraman/internal/inference"
	"gocv.io/x/gocv"
)

func newTestFramer(inW, inH, outW, outH int) *Framer {
	// smoothing=1 disables position smoothing so results are deterministic
	return NewFramer(inW, inH, outW, outH, 1.0, 1.0, 2.0, 0.25, 0.50, 1.0, 1.0)
}

func TestSelectMainFace(t *testing.T) {
	if SelectMainFace(nil) != nil {
		t.Fatal("SelectMainFace(nil) should return nil")
	}

	faces := []inference.BoundingBox{
		{X1: 0, Y1: 0, X2: 10, Y2: 10},
		{X1: 100, Y1: 100, X2: 150, Y2: 150},
		{X1: 200, Y1: 200, X2: 220, Y2: 220},
	}
	got := SelectMainFace(faces)
	if got == nil || got.X1 != 100 {
		t.Fatalf("SelectMainFace() = %+v, want the 50x50 box at (100,100)", got)
	}
}

func TestCalculateCropRegion_NilFace(t *testing.T) {
	f := newTestFramer(1280, 720, 1280, 720)
	if _, err := f.CalculateCropRegion(nil); err == nil {
		t.Fatal("expected error for nil face")
	}
}

func TestCalculateCropRegion_ZoomsInOnSmallFace(t *testing.T) {
	f := newTestFramer(1280, 720, 1280, 720)
	// Face is 10% of frame height, well below the 25% target: expect max zoom (2x)
	face := &inference.BoundingBox{X1: 600, Y1: 324, X2: 680, Y2: 396}

	crop, err := f.CalculateCropRegion(face)
	if err != nil {
		t.Fatalf("CalculateCropRegion() error: %v", err)
	}
	if crop.Dx() != 640 || crop.Dy() != 360 {
		t.Errorf("crop size = %dx%d, want 640x360 (2x zoom)", crop.Dx(), crop.Dy())
	}
	center := image.Pt((crop.Min.X+crop.Max.X)/2, (crop.Min.Y+crop.Max.Y)/2)
	if center != image.Pt(640, 360) {
		t.Errorf("crop center = %v, want face center (640,360)", center)
	}
}

func TestCalculateCropRegion_ClampsToFrameEdges(t *testing.T) {
	f := newTestFramer(1280, 720, 1280, 720)
	// Small face in the top-left corner: crop must not extend past the frame
	face := &inference.BoundingBox{X1: 0, Y1: 0, X2: 40, Y2: 40}

	crop, err := f.CalculateCropRegion(face)
	if err != nil {
		t.Fatalf("CalculateCropRegion() error: %v", err)
	}
	if !crop.In(image.Rect(0, 0, 1280, 720)) {
		t.Errorf("crop %v extends outside the frame", crop)
	}
	if crop.Min != image.Pt(0, 0) {
		t.Errorf("crop origin = %v, want (0,0)", crop.Min)
	}
}

func TestCalculateCropRegion_StaysInBoundsWithDifferentAspectRatio(t *testing.T) {
	cases := []struct {
		name               string
		inW, inH, outW, oH int
	}{
		{"4:3 in, 16:9 out", 640, 480, 1280, 720},
		{"16:9 in, 4:3 out", 1280, 720, 640, 480},
		{"16:9 in, 9:16 out", 1280, 720, 720, 1280},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTestFramer(tc.inW, tc.inH, tc.outW, tc.oH)
			// Large face forces zoom to its minimum (1x), the worst case for overflow
			face := &inference.BoundingBox{
				X1: float32(tc.inW) * 0.2, Y1: 0,
				X2: float32(tc.inW) * 0.8, Y2: float32(tc.inH),
			}

			crop, err := f.CalculateCropRegion(face)
			if err != nil {
				t.Fatalf("CalculateCropRegion() error: %v", err)
			}
			if !crop.In(image.Rect(0, 0, tc.inW, tc.inH)) {
				t.Fatalf("crop %v extends outside the %dx%d frame", crop, tc.inW, tc.inH)
			}

			wantRatio := float64(tc.outW) / float64(tc.oH)
			gotRatio := float64(crop.Dx()) / float64(crop.Dy())
			if diff := gotRatio - wantRatio; diff > 0.01 || diff < -0.01 {
				t.Errorf("crop aspect ratio = %.3f, want %.3f", gotRatio, wantRatio)
			}
		})
	}
}

func TestUpdateParameters_ClampsZoom(t *testing.T) {
	f := newTestFramer(1280, 720, 1280, 720)
	f.UpdateParameters(1.0, 1.0, 1.2, 0.25, 0.50, 1.0, 1.0)

	// Tiny face would want far more than 1.2x zoom
	face := &inference.BoundingBox{X1: 630, Y1: 350, X2: 650, Y2: 370}
	crop, err := f.CalculateCropRegion(face)
	if err != nil {
		t.Fatalf("CalculateCropRegion() error: %v", err)
	}
	if want := 1066; crop.Dx() < want {
		t.Errorf("crop width = %d, want >= %d (zoom capped at 1.2x)", crop.Dx(), want)
	}
}

func TestCropAndResize(t *testing.T) {
	f := newTestFramer(640, 480, 320, 240)
	frame := gocv.NewMatWithSize(480, 640, gocv.MatTypeCV8UC3)
	defer frame.Close()

	out, err := f.CropAndResize(frame, image.Rect(100, 100, 420, 340))
	if err != nil {
		t.Fatalf("CropAndResize() error: %v", err)
	}
	defer out.Close()
	if out.Cols() != 320 || out.Rows() != 240 {
		t.Errorf("output size = %dx%d, want 320x240", out.Cols(), out.Rows())
	}

	if _, err := f.CropAndResize(frame, image.Rect(400, 300, 800, 600)); err == nil {
		t.Error("expected error for out-of-bounds crop region")
	}
}
