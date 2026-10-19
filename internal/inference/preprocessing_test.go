package inference

import (
	"testing"

	"gocv.io/x/gocv"
)

func TestPreprocessFrameForYOLOWithInfo(t *testing.T) {
	frame := gocv.NewMatWithSize(720, 1280, gocv.MatTypeCV8UC3)
	defer frame.Close()
	frame.SetTo(gocv.NewScalar(255, 0, 0, 0)) // pure blue in BGR

	out, scale, padX, padY, err := PreprocessFrameForYOLOWithInfo(frame, 640, 640)
	if err != nil {
		t.Fatalf("PreprocessFrameForYOLOWithInfo() error: %v", err)
	}
	defer out.Close()

	if out.Cols() != 640 || out.Rows() != 640 || out.Type() != gocv.MatTypeCV32FC3 {
		t.Fatalf("output = %dx%d %v, want 640x640 CV32FC3", out.Cols(), out.Rows(), out.Type())
	}
	if scale != 0.5 || padX != 0 || padY != 140 {
		t.Errorf("letterbox = scale %v pad (%d,%d), want scale 0.5 pad (0,140)", scale, padX, padY)
	}

	// Center pixel: blue converted to RGB order and normalized to [0,1]
	px := out.GetVecfAt(320, 320)
	if px[0] != 0 || px[1] != 0 || px[2] != 1 {
		t.Errorf("center pixel RGB = %v, want [0 0 1]", px)
	}
	// Padding row: YOLO gray (114/255)
	if pad := out.GetVecfAt(10, 320); pad[0] < 0.44 || pad[0] > 0.45 {
		t.Errorf("padding pixel = %v, want ~0.447 (114/255)", pad)
	}
}
