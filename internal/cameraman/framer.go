package cameraman

import (
	"fmt"
	"image"
	"sync"

	"github.com/amir-the-h/virtual-cameraman/internal/cuda"
	"github.com/amir-the-h/virtual-cameraman/internal/inference"
	"github.com/amir-the-h/virtual-cameraman/internal/tracking"
	"gocv.io/x/gocv"
)

// Framer handles face selection, smoothing, and framing logic
type Framer struct {
	mu                 sync.Mutex
	smoother           *tracking.Smoother
	frameWidth         int
	frameHeight        int
	outputWidth        int
	outputHeight       int
	zoomMin            float32
	zoomMax            float32
	targetFaceSizeMin  float32 // Target face size as percentage of frame (e.g., 0.25 = 25%)
	targetFaceSizeMax  float32 // Maximum face size before zooming out (e.g., 0.50 = 50%)
	zoomSmoothingAlpha float32 // Smoothing factor for zoom adjustments
	faceBoxPadding     float32 // Padding multiplier for face bounding box (e.g., 1.3 = 30% larger)
	currentZoom        float32 // Current smoothed zoom level
	zoomInitialized    bool    // Whether zoom has been initialized
	aspectRatio        float32 // Output aspect ratio (width/height)
	cudaProcessor      *cuda.Processor
}

// NewFramer creates a new framer
func NewFramer(frameWidth, frameHeight, outputWidth, outputHeight int, smoothingFactor, zoomMin, zoomMax, targetFaceSizeMin, targetFaceSizeMax, zoomSmoothingFactor, faceBoxPadding float32) *Framer {
	aspectRatio := float32(outputWidth) / float32(outputHeight)

	// Try to initialize CUDA processor (graceful fallback to CPU if unavailable)
	cudaProc, _ := cuda.NewProcessor()

	// Initialize zoom to middle of range
	initialZoom := (zoomMin + zoomMax) / 2

	return &Framer{
		smoother:           tracking.NewSmoother(smoothingFactor),
		frameWidth:         frameWidth,
		frameHeight:        frameHeight,
		outputWidth:        outputWidth,
		outputHeight:       outputHeight,
		zoomMin:            zoomMin,
		zoomMax:            zoomMax,
		targetFaceSizeMin:  targetFaceSizeMin,
		targetFaceSizeMax:  targetFaceSizeMax,
		zoomSmoothingAlpha: zoomSmoothingFactor,
		faceBoxPadding:     faceBoxPadding,
		currentZoom:        initialZoom,
		zoomInitialized:    false,
		aspectRatio:        aspectRatio,
		cudaProcessor:      cudaProc,
	}
}

// UpdateDimensions updates the frame dimensions when camera resolution changes
func (f *Framer) UpdateDimensions(width, height int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frameWidth = width
	f.frameHeight = height
	// Reset zoom to middle of range for new resolution
	initialZoom := (f.zoomMin + f.zoomMax) / 2
	f.currentZoom = initialZoom
	f.zoomInitialized = false
}

// UpdateParameters updates the framer's parameters for hot-reload
func (f *Framer) UpdateParameters(smoothingFactor, zoomMin, zoomMax, targetFaceSizeMin, targetFaceSizeMax, zoomSmoothingFactor, faceBoxPadding float32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.zoomMin = zoomMin
	f.zoomMax = zoomMax
	f.targetFaceSizeMin = targetFaceSizeMin
	f.targetFaceSizeMax = targetFaceSizeMax
	f.zoomSmoothingAlpha = zoomSmoothingFactor
	f.faceBoxPadding = faceBoxPadding

	// Update smoother's alpha
	if f.smoother != nil {
		f.smoother.SetAlpha(smoothingFactor)
	}

	// Clamp current zoom to new range
	if f.currentZoom < zoomMin {
		f.currentZoom = zoomMin
	} else if f.currentZoom > zoomMax {
		f.currentZoom = zoomMax
	}
}

// SelectMainFace selects the main face from detected faces
// Strategy: largest face (by area) is typically the main subject
func SelectMainFace(faces []inference.BoundingBox) *inference.BoundingBox {
	if len(faces) == 0 {
		return nil
	}

	// Find face with largest area
	largest := &faces[0]
	maxArea := largest.Area()

	for i := 1; i < len(faces); i++ {
		area := faces[i].Area()
		if area > maxArea {
			maxArea = area
			largest = &faces[i]
		}
	}

	return largest
}

// Update processes new detections and returns the smoothed target frame
func (f *Framer) Update(detections []inference.BoundingBox) *inference.BoundingBox {
	// Select main face
	mainFace := SelectMainFace(detections)
	if mainFace == nil {
		// No face detected - return nil to indicate no crop needed
		return nil
	}

	// Apply smoothing (the smoother is not safe for concurrent use; hot-reload
	// changes its alpha under the same lock)
	f.mu.Lock()
	smoothed := f.smoother.Update(*mainFace)
	f.mu.Unlock()
	return &smoothed
}

// CalculateCropRegion calculates the crop region for the given face bounding box
// Returns: crop rectangle (x, y, width, height) in original frame coordinates
func (f *Framer) CalculateCropRegion(face *inference.BoundingBox) (image.Rectangle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if face == nil {
		return image.Rectangle{}, fmt.Errorf("framer: no face provided")
	}

	// Apply padding to face box to account for tight detection boxes
	// Expand the box outward from center by the padding factor
	faceHeight := face.Y2 - face.Y1
	faceCenterX := (face.X1 + face.X2) / 2
	faceCenterY := (face.Y1 + face.Y2) / 2

	// Expand box by padding factor (e.g., 1.3 = 30% larger)
	// We use height for zoom calculations, padding makes the face appear larger
	paddedFaceHeight := faceHeight * f.faceBoxPadding

	// Calculate current face size as percentage of frame (use height as reference)
	faceSizeRatio := paddedFaceHeight / float32(f.frameHeight)

	// Determine desired zoom based on face distance
	// If face is too small (< targetFaceSizeMin), zoom in
	// If face is too large (> targetFaceSizeMax), zoom out
	// Otherwise, maintain current zoom
	var desiredZoom float32

	if faceSizeRatio < f.targetFaceSizeMin {
		// Face is too far - zoom in (increase zoom)
		// Calculate zoom needed to bring face to target size
		// If face is 20% of frame and target is 25%, we need zoom = 25/20 = 1.25x
		desiredZoom = f.targetFaceSizeMin / faceSizeRatio
		// Clamp to reasonable range
		if desiredZoom < f.zoomMin {
			desiredZoom = f.zoomMin
		}
		if desiredZoom > f.zoomMax {
			desiredZoom = f.zoomMax
		}
	} else if faceSizeRatio > f.targetFaceSizeMax {
		// Face is too close - zoom out (decrease zoom)
		// Calculate zoom needed to bring face to target size
		// If face is 70% of frame and target max is 65%, we need zoom = 65/70 = 0.93x
		desiredZoom = f.targetFaceSizeMax / faceSizeRatio
		// Clamp to reasonable range
		if desiredZoom < f.zoomMin {
			desiredZoom = f.zoomMin
		}
		if desiredZoom > f.zoomMax {
			desiredZoom = f.zoomMax
		}
	} else {
		// Face is in target range - maintain current zoom or slightly adjust
		// Use current zoom, but allow small adjustments based on face size
		desiredZoom = f.currentZoom
		// Small adjustment to keep face centered in target range
		targetMid := (f.targetFaceSizeMin + f.targetFaceSizeMax) / 2
		if faceSizeRatio < targetMid {
			// Slightly zoom in
			desiredZoom = desiredZoom * 1.02
		} else {
			// Slightly zoom out
			desiredZoom = desiredZoom * 0.98
		}
		// Clamp adjustments to valid range
		if desiredZoom < f.zoomMin {
			desiredZoom = f.zoomMin
		}
		if desiredZoom > f.zoomMax {
			desiredZoom = f.zoomMax
		}
	}

	// Clamp desired zoom to valid range
	if desiredZoom < f.zoomMin {
		desiredZoom = f.zoomMin
	}
	if desiredZoom > f.zoomMax {
		desiredZoom = f.zoomMax
	}

	// Apply smoothing to zoom changes to avoid jitter
	if !f.zoomInitialized {
		// First frame: use desired zoom directly
		f.currentZoom = desiredZoom
		f.zoomInitialized = true
	} else {
		// Apply EMA smoothing: smoothed = alpha * new + (1 - alpha) * old
		f.currentZoom = f.zoomSmoothingAlpha*desiredZoom + (1-f.zoomSmoothingAlpha)*f.currentZoom
	}

	zoom := f.currentZoom

	// Calculate crop dimensions maintaining aspect ratio
	cropWidth := float32(f.frameWidth) / zoom
	cropHeight := float32(f.frameHeight) / zoom

	// Ensure crop maintains output aspect ratio
	if cropWidth/cropHeight > f.aspectRatio {
		// Crop is too wide, adjust height
		cropHeight = cropWidth / f.aspectRatio
	} else {
		// Crop is too tall, adjust width
		cropWidth = cropHeight * f.aspectRatio
	}

	// When the output aspect ratio differs from the input, the adjustment above
	// can grow the crop past the frame; shrink it to fit, keeping the aspect ratio
	if cropWidth > float32(f.frameWidth) {
		cropHeight *= float32(f.frameWidth) / cropWidth
		cropWidth = float32(f.frameWidth)
	}
	if cropHeight > float32(f.frameHeight) {
		cropWidth *= float32(f.frameHeight) / cropHeight
		cropHeight = float32(f.frameHeight)
	}

	// Center crop on face (use original face center, not padded)
	cropX := faceCenterX - cropWidth/2
	cropY := faceCenterY - cropHeight/2

	// Clamp to frame boundaries
	if cropX < 0 {
		cropX = 0
	}
	if cropY < 0 {
		cropY = 0
	}
	if cropX+cropWidth > float32(f.frameWidth) {
		cropX = float32(f.frameWidth) - cropWidth
	}
	if cropY+cropHeight > float32(f.frameHeight) {
		cropY = float32(f.frameHeight) - cropHeight
	}

	// Ensure crop is still within bounds after clamping
	if cropX < 0 {
		cropX = 0
	}
	if cropY < 0 {
		cropY = 0
	}

	return image.Rect(
		int(cropX),
		int(cropY),
		int(cropX+cropWidth),
		int(cropY+cropHeight),
	), nil
}

// CropAndResize crops and resizes a frame based on the crop region
func (f *Framer) CropAndResize(frame gocv.Mat, cropRegion image.Rectangle) (gocv.Mat, error) {
	if frame.Empty() {
		return gocv.Mat{}, fmt.Errorf("framer: empty input frame")
	}

	// Validate crop region
	if cropRegion.Min.X < 0 || cropRegion.Min.Y < 0 ||
		cropRegion.Max.X > frame.Cols() || cropRegion.Max.Y > frame.Rows() {
		return gocv.Mat{}, fmt.Errorf("framer: crop region out of bounds")
	}

	// Crop the region
	cropped := frame.Region(cropRegion)
	defer cropped.Close()

	// Resize to output dimensions (use CUDA if available)
	resized := gocv.NewMat()
	if f.cudaProcessor != nil && f.cudaProcessor.IsEnabled() {
		err := f.cudaProcessor.Resize(cropped, &resized, image.Pt(f.outputWidth, f.outputHeight))
		if err != nil {
			// Fallback to CPU if CUDA fails
			gocv.Resize(cropped, &resized, image.Pt(f.outputWidth, f.outputHeight), 0, 0, gocv.InterpolationLinear)
		}
	} else {
		// Use CPU
		gocv.Resize(cropped, &resized, image.Pt(f.outputWidth, f.outputHeight), 0, 0, gocv.InterpolationLinear)
	}

	return resized, nil
}

// Reset clears the framer state
func (f *Framer) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.smoother.Reset()
	f.zoomInitialized = false
	f.currentZoom = (f.zoomMin + f.zoomMax) / 2
}

// OutputWidth returns the output frame width
func (f *Framer) OutputWidth() int {
	return f.outputWidth
}

// OutputHeight returns the output frame height
func (f *Framer) OutputHeight() int {
	return f.outputHeight
}
