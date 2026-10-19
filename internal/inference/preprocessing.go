package inference

import (
	"fmt"
	"image"
	"image/color"

	"gocv.io/x/gocv"
)

// PreprocessFrameForYOLO preprocesses a frame specifically for YOLO models
// YOLO models typically expect:
// - RGB input
// - Normalized to [0, 1] or mean/std normalization
// - Specific input size (e.g., 640x640)
// Deprecated: Use PreprocessFrameForYOLOWithInfo instead
func PreprocessFrameForYOLO(frame gocv.Mat, targetWidth, targetHeight int) (gocv.Mat, error) {
	mat, _, _, _, err := PreprocessFrameForYOLOWithInfo(frame, targetWidth, targetHeight)
	return mat, err
}

// PreprocessFrameForYOLOWithInfo preprocesses a frame for YOLO and returns letterbox info
// Returns: (preprocessed Mat, scale, padX, padY, error)
func PreprocessFrameForYOLOWithInfo(frame gocv.Mat, targetWidth, targetHeight int) (gocv.Mat, float64, int, int, error) {
	if frame.Empty() {
		return gocv.Mat{}, 0, 0, 0, fmt.Errorf("preprocess yolo: empty input frame")
	}

	// Resize maintaining aspect ratio with letterboxing (YOLO style)
	resized, scale, padX, padY := letterboxResize(frame, targetWidth, targetHeight)
	defer resized.Close()

	// Convert BGR to RGB
	rgb := gocv.NewMat()
	defer rgb.Close()
	gocv.CvtColor(resized, &rgb, gocv.ColorBGRToRGB)

	// Convert to float32 and normalize to [0, 1] in a single pass
	normalized := gocv.NewMat()
	rgb.ConvertToWithParams(&normalized, gocv.MatTypeCV32F, 1.0/255.0, 0)

	return normalized, scale, padX, padY, nil
}

// letterboxResize resizes an image maintaining aspect ratio with padding
// Returns the resized image, scale factor, and padding offsets
func letterboxResize(img gocv.Mat, targetWidth, targetHeight int) (gocv.Mat, float64, int, int) {
	imgWidth := img.Cols()
	imgHeight := img.Rows()

	// Calculate scale to fit image into target size
	scale := float64(targetWidth) / float64(imgWidth)
	if float64(targetHeight)/float64(imgHeight) < scale {
		scale = float64(targetHeight) / float64(imgHeight)
	}

	newWidth := int(float64(imgWidth) * scale)
	newHeight := int(float64(imgHeight) * scale)

	// Resize
	resized := gocv.NewMat()
	gocv.Resize(img, &resized, image.Pt(newWidth, newHeight), 0, 0, gocv.InterpolationLinear)

	// Calculate padding
	padX := (targetWidth - newWidth) / 2
	padY := (targetHeight - newHeight) / 2

	// Create letterboxed image with padding
	// YOLO uses gray padding (114, 114, 114) for letterboxing
	borderColor := color.RGBA{R: 114, G: 114, B: 114, A: 0}
	letterboxed := gocv.NewMat()
	gocv.CopyMakeBorder(resized, &letterboxed, padY, targetHeight-newHeight-padY,
		padX, targetWidth-newWidth-padX, gocv.BorderConstant, borderColor)
	resized.Close()

	return letterboxed, scale, padX, padY
}
