package inference

import (
	"fmt"
	"log"
)

// DetectorFactory creates the appropriate detector based on availability
type DetectorFactory struct {
	modelPath           string
	confidenceThreshold float32
	inputWidth          int
	inputHeight         int
}

// NewDetectorFactory creates a factory for detectors
func NewDetectorFactory(modelPath string, confidenceThreshold float32, inputWidth, inputHeight int) *DetectorFactory {
	return &DetectorFactory{
		modelPath:           modelPath,
		confidenceThreshold: confidenceThreshold,
		inputWidth:          inputWidth,
		inputHeight:         inputHeight,
	}
}

// CreateDetector creates an ONNX detector with CUDA fallback to CPU
func (df *DetectorFactory) CreateDetector() (Detector, error) {
	// Try ONNX with CUDA first
	detector, err := df.createOnnxDetector()
	if err == nil {
		log.Printf("Using ONNX detector with provider: %s", detector.GetExecutionProvider())
		return detector, nil
	}

	// Log CUDA attempt failure
	log.Printf("ONNX detector creation failed: %v", err)
	log.Println("Falling back to CPU-only detector")

	// Fallback to CPU
	fallback := NewFallbackDetector()
	log.Printf("Using fallback detector: %s", fallback.GetExecutionProvider())
	return fallback, nil
}

// createOnnxDetector attempts to create an ONNX detector
func (df *DetectorFactory) createOnnxDetector() (Detector, error) {
	// Try to create ONNX detector
	// ONNXRUNTIME_HOME is optional if ONNX Runtime is system-installed
	detector, err := NewOnnxDetector(df.modelPath, df.confidenceThreshold, df.inputWidth, df.inputHeight)
	if err != nil {
		return nil, fmt.Errorf("onnx detector: %w", err)
	}

	return detector, nil
}
