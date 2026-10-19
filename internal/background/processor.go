package background

import (
	"fmt"
	"image"
	"image/color"

	"github.com/amir-the-h/virtual-cameraman/internal/inference"
	"gocv.io/x/gocv"
)

// Processor handles background blur and replacement
type Processor struct {
	enabled         bool
	mode            string // "blur" or "replace"
	blurStrength    int    // Gaussian blur kernel size (must be odd, e.g., 15, 21, 31)
	backgroundImage *gocv.Mat
	backgroundColor color.RGBA
	facePadding     float32 // Padding around face for mask (e.g., 1.2 = 20% larger)
}

// Mode constants
const (
	ModeBlur    = "blur"
	ModeReplace = "replace"
)

// NewProcessor creates a new background processor
func NewProcessor(enabled bool, mode string, blurStrength int, bgImagePath string, bgColor color.RGBA, facePadding float32) (*Processor, error) {
	p := &Processor{
		enabled:         enabled,
		mode:            mode,
		blurStrength:    blurStrength,
		backgroundColor: bgColor,
		facePadding:     facePadding,
	}

	if !enabled {
		return p, nil
	}

	// Load background image if provided
	if mode == ModeReplace && bgImagePath != "" {
		img := gocv.IMRead(bgImagePath, gocv.IMReadColor)
		if img.Empty() {
			return nil, fmt.Errorf("failed to load background image: %s", bgImagePath)
		}
		p.backgroundImage = &img
	}

	// Ensure blur strength is odd (required for Gaussian blur)
	if blurStrength%2 == 0 {
		p.blurStrength = blurStrength + 1
	}

	return p, nil
}

// Process applies background blur or replacement to the frame
// detections: face bounding boxes to create mask from
// Returns: processed frame (new Mat, caller must close)
func (p *Processor) Process(frame gocv.Mat, detections []inference.BoundingBox) (gocv.Mat, error) {
	if !p.enabled || frame.Empty() {
		// Return a clone if disabled (caller still needs to manage it)
		return frame.Clone(), nil
	}

	if len(detections) == 0 {
		// No faces detected - apply effect to entire frame
		if p.mode == ModeBlur {
			return p.blurFrame(frame), nil
		} else {
			return p.replaceBackground(frame, nil), nil
		}
	}

	// Create mask from face detections
	mask := p.createFaceMask(frame, detections)

	// Apply effect based on mode
	var result gocv.Mat
	if p.mode == ModeBlur {
		result = p.applyBlurWithMask(frame, mask)
	} else {
		result = p.replaceBackgroundWithMask(frame, mask)
	}

	mask.Close()
	return result, nil
}

// createFaceMask creates a mask where face regions are white (255) and background is black (0)
func (p *Processor) createFaceMask(frame gocv.Mat, detections []inference.BoundingBox) gocv.Mat {
	mask := gocv.NewMatWithSize(frame.Rows(), frame.Cols(), gocv.MatTypeCV8UC1)
	mask.SetTo(gocv.NewScalar(0, 0, 0, 0)) // Start with black (background)

	// Draw white ellipses/rectangles for each face (with padding)
	for _, box := range detections {
		// Expand box by padding factor
		width := box.X2 - box.X1
		height := box.Y2 - box.Y1
		centerX := (box.X1 + box.X2) / 2
		centerY := (box.Y1 + box.Y2) / 2

		// Apply padding
		paddedWidth := width * p.facePadding
		paddedHeight := height * p.facePadding
		paddedX1 := centerX - paddedWidth/2
		paddedY1 := centerY - paddedHeight/2
		paddedX2 := centerX + paddedWidth/2
		paddedY2 := centerY + paddedHeight/2

		// Create rectangle for face mask (filled = -1 thickness)
		rect := image.Rect(
			int(paddedX1), int(paddedY1),
			int(paddedX2), int(paddedY2),
		)
		whiteColor := color.RGBA{R: 255, G: 255, B: 255, A: 255}
		gocv.Rectangle(&mask, rect, whiteColor, -1)
	}

	return mask
}

// blurFrame applies Gaussian blur to the entire frame
func (p *Processor) blurFrame(frame gocv.Mat) gocv.Mat {
	blurred := gocv.NewMat()
	gocv.GaussianBlur(frame, &blurred, image.Pt(p.blurStrength, p.blurStrength), 0, 0, gocv.BorderDefault)
	return blurred
}

// applyBlurWithMask blurs the background (everything outside the mask)
func (p *Processor) applyBlurWithMask(frame gocv.Mat, mask gocv.Mat) gocv.Mat {
	// Blur the entire frame
	blurred := p.blurFrame(frame)
	defer blurred.Close()

	// Create inverse mask (background = white, face = black)
	inverseMask := gocv.NewMat()
	gocv.BitwiseNot(mask, &inverseMask)
	defer inverseMask.Close()

	// Convert mask to 3 channels for blending
	mask3ch := gocv.NewMat()
	gocv.CvtColor(mask, &mask3ch, gocv.ColorGrayToBGR)
	defer mask3ch.Close()

	inverseMask3ch := gocv.NewMat()
	gocv.CvtColor(inverseMask, &inverseMask3ch, gocv.ColorGrayToBGR)
	defer inverseMask3ch.Close()

	// Blend: face from original, background from blurred
	result := gocv.NewMat()

	// Face region (from original frame)
	faceRegion := gocv.NewMat()
	gocv.BitwiseAnd(frame, mask3ch, &faceRegion)
	defer faceRegion.Close()

	// Background region (from blurred frame)
	bgRegion := gocv.NewMat()
	gocv.BitwiseAnd(blurred, inverseMask3ch, &bgRegion)
	defer bgRegion.Close()

	// Combine face and background
	gocv.BitwiseOr(faceRegion, bgRegion, &result)

	return result
}

// replaceBackground replaces background with solid color or image
func (p *Processor) replaceBackground(frame gocv.Mat, mask *gocv.Mat) gocv.Mat {
	result := gocv.NewMat()

	if p.backgroundImage != nil && !p.backgroundImage.Empty() {
		// Replace with image
		bgImg := *p.backgroundImage

		// Resize background image to match frame size
		resizedBg := gocv.NewMat()
		gocv.Resize(bgImg, &resizedBg, image.Pt(frame.Cols(), frame.Rows()), 0, 0, gocv.InterpolationLinear)
		defer resizedBg.Close()

		if mask != nil {
			// Use mask to blend
			mask3ch := gocv.NewMat()
			gocv.CvtColor(*mask, &mask3ch, gocv.ColorGrayToBGR)
			defer mask3ch.Close()

			inverseMask3ch := gocv.NewMat()
			inverseMask := gocv.NewMat()
			gocv.BitwiseNot(*mask, &inverseMask)
			gocv.CvtColor(inverseMask, &inverseMask3ch, gocv.ColorGrayToBGR)
			defer inverseMask.Close()
			defer inverseMask3ch.Close()

			// Face from original, background from image
			faceRegion := gocv.NewMat()
			gocv.BitwiseAnd(frame, mask3ch, &faceRegion)
			defer faceRegion.Close()

			bgRegion := gocv.NewMat()
			gocv.BitwiseAnd(resizedBg, inverseMask3ch, &bgRegion)
			defer bgRegion.Close()

			gocv.BitwiseOr(faceRegion, bgRegion, &result)
		} else {
			// No mask - use background image directly
			resizedBg.CopyTo(&result)
		}
	} else {
		// Replace with solid color
		if mask != nil {
			mask3ch := gocv.NewMat()
			gocv.CvtColor(*mask, &mask3ch, gocv.ColorGrayToBGR)
			defer mask3ch.Close()

			inverseMask3ch := gocv.NewMat()
			inverseMask := gocv.NewMat()
			gocv.BitwiseNot(*mask, &inverseMask)
			gocv.CvtColor(inverseMask, &inverseMask3ch, gocv.ColorGrayToBGR)
			defer inverseMask.Close()
			defer inverseMask3ch.Close()

			// Create solid color background
			bgColor := gocv.NewMatWithSize(frame.Rows(), frame.Cols(), gocv.MatTypeCV8UC3)
			bgColor.SetTo(gocv.NewScalar(
				float64(p.backgroundColor.B),
				float64(p.backgroundColor.G),
				float64(p.backgroundColor.R),
				0,
			))

			// Face from original, background from solid color
			faceRegion := gocv.NewMat()
			gocv.BitwiseAnd(frame, mask3ch, &faceRegion)
			defer faceRegion.Close()

			bgRegion := gocv.NewMat()
			gocv.BitwiseAnd(bgColor, inverseMask3ch, &bgRegion)
			defer bgColor.Close()
			defer bgRegion.Close()

			gocv.BitwiseOr(faceRegion, bgRegion, &result)
		} else {
			// No mask - fill entire frame with color
			result = gocv.NewMatWithSize(frame.Rows(), frame.Cols(), gocv.MatTypeCV8UC3)
			result.SetTo(gocv.NewScalar(
				float64(p.backgroundColor.B),
				float64(p.backgroundColor.G),
				float64(p.backgroundColor.R),
				0,
			))
		}
	}

	return result
}

// replaceBackgroundWithMask replaces background using a mask
func (p *Processor) replaceBackgroundWithMask(frame gocv.Mat, mask gocv.Mat) gocv.Mat {
	return p.replaceBackground(frame, &mask)
}

// Close releases resources
func (p *Processor) Close() {
	if p.backgroundImage != nil {
		p.backgroundImage.Close()
	}
}
