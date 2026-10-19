package inference

import (
	"sort"
)

// NMS performs Non-Maximum Suppression on bounding boxes
// boxes: input bounding boxes
// scores: confidence scores for each box
// iouThreshold: IoU threshold for NMS (typically 0.4-0.5)
// Returns: indices of boxes to keep
func NMS(boxes []BoundingBox, scores []float32, iouThreshold float32) []int {
	if len(boxes) == 0 {
		return []int{}
	}

	// Create indices and sort by score (descending)
	type boxScore struct {
		idx   int
		score float32
	}
	boxScores := make([]boxScore, len(boxes))
	for i := range boxes {
		boxScores[i] = boxScore{idx: i, score: scores[i]}
	}

	// Sort by score descending
	sort.Slice(boxScores, func(i, j int) bool {
		return boxScores[i].score > boxScores[j].score
	})

	// Apply NMS
	keep := make([]int, 0)
	suppressed := make([]bool, len(boxes))

	for i := 0; i < len(boxScores); i++ {
		if suppressed[boxScores[i].idx] {
			continue
		}

		keep = append(keep, boxScores[i].idx)
		currentBox := boxes[boxScores[i].idx]

		// Suppress boxes with high IoU
		for j := i + 1; j < len(boxScores); j++ {
			if suppressed[boxScores[j].idx] {
				continue
			}

			otherBox := boxes[boxScores[j].idx]
			iou := calculateIoU(currentBox, otherBox)

			if iou > iouThreshold {
				suppressed[boxScores[j].idx] = true
			}
		}
	}

	return keep
}

// calculateIoU calculates Intersection over Union between two bounding boxes
func calculateIoU(box1, box2 BoundingBox) float32 {
	// Calculate intersection
	x1 := max(box1.X1, box2.X1)
	y1 := max(box1.Y1, box2.Y1)
	x2 := min(box1.X2, box2.X2)
	y2 := min(box1.Y2, box2.Y2)

	if x2 <= x1 || y2 <= y1 {
		return 0.0
	}

	intersection := (x2 - x1) * (y2 - y1)

	// Calculate union
	area1 := box1.Area()
	area2 := box2.Area()
	union := area1 + area2 - intersection

	if union <= 0 {
		return 0.0
	}

	return intersection / union
}

// FilterByConfidence filters bounding boxes by confidence threshold
func FilterByConfidence(boxes []BoundingBox, scores []float32, threshold float32) ([]BoundingBox, []float32) {
	if len(boxes) != len(scores) {
		return []BoundingBox{}, []float32{}
	}

	filteredBoxes := make([]BoundingBox, 0)
	filteredScores := make([]float32, 0)

	for i := range boxes {
		if scores[i] >= threshold {
			filteredBoxes = append(filteredBoxes, boxes[i])
			filteredScores = append(filteredScores, scores[i])
		}
	}

	return filteredBoxes, filteredScores
}

// ScaleBoxes scales bounding boxes from model input size to original frame size
func ScaleBoxes(boxes []BoundingBox, scaleX, scaleY float32, offsetX, offsetY int) []BoundingBox {
	scaled := make([]BoundingBox, len(boxes))
	for i := range boxes {
		scaled[i] = BoundingBox{
			X1:         (boxes[i].X1 - float32(offsetX)) * scaleX,
			Y1:         (boxes[i].Y1 - float32(offsetY)) * scaleY,
			X2:         (boxes[i].X2 - float32(offsetX)) * scaleX,
			Y2:         (boxes[i].Y2 - float32(offsetY)) * scaleY,
			Confidence: boxes[i].Confidence,
		}
	}
	return scaled
}

// ClampBoxes ensures bounding boxes are within frame bounds
func ClampBoxes(boxes []BoundingBox, frameWidth, frameHeight int) []BoundingBox {
	clamped := make([]BoundingBox, len(boxes))
	for i := range boxes {
		clamped[i] = BoundingBox{
			X1:         max(0, min(boxes[i].X1, float32(frameWidth))),
			Y1:         max(0, min(boxes[i].Y1, float32(frameHeight))),
			X2:         max(0, min(boxes[i].X2, float32(frameWidth))),
			Y2:         max(0, min(boxes[i].Y2, float32(frameHeight))),
			Confidence: boxes[i].Confidence,
		}
	}
	return clamped
}

// Helper functions
func max(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func min(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

// YOLOv11PostProcess processes YOLOv11 raw outputs into bounding boxes
// Deprecated: Use YOLOv11PostProcessWithLetterbox for correct coordinate mapping
func YOLOv11PostProcess(
	rawOutput []float32,
	inputWidth, inputHeight int,
	frameWidth, frameHeight int,
	confidenceThreshold, nmsThreshold float32,
) []BoundingBox {
	return YOLOv11PostProcessWithLetterbox(
		rawOutput,
		inputWidth, inputHeight,
		frameWidth, frameHeight,
		confidenceThreshold, nmsThreshold,
		0, 0, 0, // No letterbox correction
	)
}

// YOLOv11PostProcessWithLetterbox processes YOLOv11 outputs with letterbox correction
// This function properly handles the letterbox padding added during preprocessing
// rawOutput: raw model output tensor (shape: [1, num_detections, 6] or similar)
//
//	Format: [batch, detections, (x1, y1, x2, y2, confidence, class)]
//
// inputWidth, inputHeight: model input dimensions
// frameWidth, frameHeight: original frame dimensions
// confidenceThreshold: minimum confidence to keep
// nmsThreshold: IoU threshold for NMS
// letterboxScale: scale factor from letterbox resize
// letterboxPadX, letterboxPadY: padding offsets from letterbox
func YOLOv11PostProcessWithLetterbox(
	rawOutput []float32,
	inputWidth, inputHeight int,
	frameWidth, frameHeight int,
	confidenceThreshold, nmsThreshold float32,
	letterboxScale float64,
	letterboxPadX, letterboxPadY int,
) []BoundingBox {
	if len(rawOutput) == 0 {
		return []BoundingBox{}
	}

	// YOLOv11 face detection models output [batch, features, anchors] = [1, 5, 8400]
	// Where 5 features = [x_center, y_center, width, height, confidence]
	// For 640x640 input, num_anchors is typically 8400
	// The flattened output is [1 * 5 * 8400] = 42000 values
	// Format: Transposed - all x_centers, then all y_centers, then all widths, etc.
	// rawOutput[0:8400] = x_centers for all anchors
	// rawOutput[8400:16800] = y_centers for all anchors
	// rawOutput[16800:25200] = widths for all anchors
	// rawOutput[25200:33600] = heights for all anchors
	// rawOutput[33600:42000] = confidences for all anchors

	numAnchors := 8400
	if len(rawOutput) < 5*numAnchors {
		// Try to infer num_anchors from output size
		numAnchors = len(rawOutput) / 5
		if numAnchors == 0 {
			return []BoundingBox{}
		}
	}

	boxes := make([]BoundingBox, 0, numAnchors)
	scores := make([]float32, 0, numAnchors)

	// Extract feature arrays (transposed format)
	xCenters := rawOutput[0*numAnchors : 1*numAnchors]
	yCenters := rawOutput[1*numAnchors : 2*numAnchors]
	widths := rawOutput[2*numAnchors : 3*numAnchors]
	heights := rawOutput[3*numAnchors : 4*numAnchors]
	confidences := rawOutput[4*numAnchors : 5*numAnchors]

	// Process each anchor
	for i := 0; i < numAnchors; i++ {
		if i >= len(confidences) {
			break
		}

		confidence := confidences[i]

		// Skip if confidence is too low
		if confidence < confidenceThreshold {
			continue
		}

		// Get values for this anchor
		xCenter := xCenters[i]
		yCenter := yCenters[i]
		width := widths[i]
		height := heights[i]

		// Convert center+size to corner format (in input image coordinates)
		x1 := xCenter - width/2
		y1 := yCenter - height/2
		x2 := xCenter + width/2
		y2 := yCenter + height/2

		// Remove letterbox padding if present
		if letterboxScale > 0 {
			// Subtract padding to get coordinates in the non-padded resized image
			x1 -= float32(letterboxPadX)
			y1 -= float32(letterboxPadY)
			x2 -= float32(letterboxPadX)
			y2 -= float32(letterboxPadY)

			// Scale from resized image to original frame
			// letterboxScale = resizedSize / originalSize, so we divide by it
			x1 /= float32(letterboxScale)
			y1 /= float32(letterboxScale)
			x2 /= float32(letterboxScale)
			y2 /= float32(letterboxScale)
		} else {
			// Fallback: simple scale from model input to frame (old behavior)
			scaleX := float32(frameWidth) / float32(inputWidth)
			scaleY := float32(frameHeight) / float32(inputHeight)
			x1 *= scaleX
			y1 *= scaleY
			x2 *= scaleX
			y2 *= scaleY
		}

		box := BoundingBox{
			X1:         x1,
			Y1:         y1,
			X2:         x2,
			Y2:         y2,
			Confidence: confidence,
		}

		boxes = append(boxes, box)
		scores = append(scores, confidence)
	}

	// Apply NMS
	if len(boxes) > 0 && nmsThreshold > 0 {
		keepIndices := NMS(boxes, scores, nmsThreshold)
		filteredBoxes := make([]BoundingBox, len(keepIndices))
		for i, idx := range keepIndices {
			filteredBoxes[i] = boxes[idx]
		}
		boxes = filteredBoxes
	}

	// Clamp to frame bounds
	boxes = ClampBoxes(boxes, frameWidth, frameHeight)

	return boxes
}
