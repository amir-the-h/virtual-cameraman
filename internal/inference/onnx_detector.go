package inference

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/yalue/onnxruntime_go"
	"gocv.io/x/gocv"
)

var (
	onnxInitialized bool
	onnxInitMutex   sync.Mutex
)

// OnnxDetector wraps ONNX Runtime for face detection
type OnnxDetector struct {
	modelPath           string
	executionProvider   string
	inferenceTime       float32
	inputWidth          int
	inputHeight         int
	confidenceThreshold float32
	nmsThreshold        float32
	metrics             *MetricsCollector
	session             *onnxruntime_go.AdvancedSession
	inputTensor         *onnxruntime_go.Tensor[float32]
	outputTensor        *onnxruntime_go.Tensor[float32]
	inputName           string
	outputName          string
	sessionInitialized  bool
	// Letterbox padding info (updated per-frame)
	letterboxScale float64
	letterboxPadX  int
	letterboxPadY  int
}

// initializeONNXRuntime initializes ONNX Runtime (thread-safe, only once)
func initializeONNXRuntime() error {
	onnxInitMutex.Lock()
	defer onnxInitMutex.Unlock()

	if onnxInitialized {
		return nil
	}

	// Set library path if not already set via environment variable
	// Check for ONNXRUNTIME_SHARED_LIBRARY_PATH env var first
	libPath := os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH")
	if libPath == "" {
		// Try common system locations
		commonPaths := []string{
			"/usr/lib/libonnxruntime.so",
			"/usr/lib/libonnxruntime.so.1",
			"/usr/local/lib/libonnxruntime.so",
		}
		for _, path := range commonPaths {
			if _, err := os.Stat(path); err == nil {
				onnxruntime_go.SetSharedLibraryPath(path)
				log.Printf("ONNX: Using library at %s", path)
				break
			}
		}
	} else {
		onnxruntime_go.SetSharedLibraryPath(libPath)
		log.Printf("ONNX: Using library from ONNXRUNTIME_SHARED_LIBRARY_PATH: %s", libPath)
	}

	err := onnxruntime_go.InitializeEnvironment()
	if err != nil {
		return fmt.Errorf("failed to initialize ONNX Runtime: %w", err)
	}

	onnxInitialized = true
	return nil
}

// NewOnnxDetector creates an ONNX detector
// modelPath can be an absolute path or relative to ONNXRUNTIME_HOME
func NewOnnxDetector(modelPath string, confidenceThreshold float32, inputWidth, inputHeight int) (*OnnxDetector, error) {
	if modelPath == "" {
		return nil, fmt.Errorf("onnx detector: model path cannot be empty")
	}

	// Resolve model path
	resolvedPath, err := resolveModelPath(modelPath)
	if err != nil {
		return nil, fmt.Errorf("onnx detector: resolve model path: %w", err)
	}

	if _, err := os.Stat(resolvedPath); err != nil {
		return nil, fmt.Errorf("onnx detector: model file not found at %s: %w", resolvedPath, err)
	}

	// Initialize ONNX Runtime
	if err := initializeONNXRuntime(); err != nil {
		return nil, fmt.Errorf("onnx detector: initialize runtime: %w", err)
	}

	// Get input/output names from model
	inputInfos, outputInfos, err := onnxruntime_go.GetInputOutputInfo(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("onnx detector: get model info: %w", err)
	}

	if len(inputInfos) == 0 || len(outputInfos) == 0 {
		return nil, fmt.Errorf("onnx detector: model has no inputs or outputs")
	}

	inputName := inputInfos[0].Name
	outputName := outputInfos[0].Name

	// Create input tensor (NCHW format: [1, 3, height, width])
	inputShape := onnxruntime_go.NewShape(1, 3, int64(inputHeight), int64(inputWidth))
	inputData := make([]float32, 1*3*inputHeight*inputWidth)
	inputTensor, err := onnxruntime_go.NewTensor(inputShape, inputData)
	if err != nil {
		return nil, fmt.Errorf("onnx detector: create input tensor: %w", err)
	}

	// Create output tensor
	// YOLOv11 face detection models output [1, 5, num_anchors] (transposed format)
	// Where 5 = [x_center, y_center, width, height, confidence]
	// For 640x640 input, num_anchors is typically 8400
	// Format: [batch, features, anchors] = [1, 5, 8400]
	outputShape := onnxruntime_go.NewShape(1, 5, 8400)
	outputData := make([]float32, 1*5*8400)
	outputTensor, err := onnxruntime_go.NewTensor(outputShape, outputData)
	if err != nil {
		inputTensor.Destroy()
		return nil, fmt.Errorf("onnx detector: create output tensor: %w", err)
	}

	// Create session options
	opts, err := onnxruntime_go.NewSessionOptions()
	if err != nil {
		inputTensor.Destroy()
		outputTensor.Destroy()
		return nil, fmt.Errorf("onnx detector: create session options: %w", err)
	}
	defer opts.Destroy()

	// Try to use CUDA if available, fallback to CPU
	executionProvider := "CPU"
	cudaOpts, err := onnxruntime_go.NewCUDAProviderOptions()
	if err == nil {
		defer cudaOpts.Destroy()
		if err := opts.AppendExecutionProviderCUDA(cudaOpts); err == nil {
			executionProvider = "CUDA"
			log.Println("ONNX: Using CUDA execution provider")
		} else {
			log.Printf("ONNX: CUDA provider failed, using CPU: %v", err)
		}
	} else {
		log.Printf("ONNX: CUDA not available, using CPU: %v", err)
	}

	// Create advanced session
	session, err := onnxruntime_go.NewAdvancedSession(
		resolvedPath,
		[]string{inputName},
		[]string{outputName},
		[]onnxruntime_go.Value{inputTensor},
		[]onnxruntime_go.Value{outputTensor},
		opts,
	)
	if err != nil {
		inputTensor.Destroy()
		outputTensor.Destroy()
		return nil, fmt.Errorf("onnx detector: create session: %w", err)
	}

	detector := &OnnxDetector{
		modelPath:           resolvedPath,
		executionProvider:   executionProvider,
		inferenceTime:       0,
		inputWidth:          inputWidth,
		inputHeight:         inputHeight,
		confidenceThreshold: confidenceThreshold,
		nmsThreshold:        0.4, // Default NMS threshold
		metrics:             NewMetricsCollector(),
		session:             session,
		inputTensor:         inputTensor,
		outputTensor:        outputTensor,
		inputName:           inputName,
		outputName:          outputName,
		sessionInitialized:  true,
	}

	log.Printf("ONNX Detector initialized (model: %s, provider: %s, confidence: %.2f)",
		resolvedPath, executionProvider, confidenceThreshold)

	return detector, nil
}

// DetectFaces performs face detection on a frame
func (o *OnnxDetector) DetectFaces(frame gocv.Mat) ([]BoundingBox, error) {
	if frame.Empty() {
		return nil, fmt.Errorf("onnx detector: empty frame")
	}

	if !o.sessionInitialized || o.session == nil {
		return nil, fmt.Errorf("onnx detector: session not initialized")
	}

	start := time.Now()
	defer func() {
		o.inferenceTime = float32(time.Since(start).Milliseconds())
		o.metrics.InferenceTime = o.inferenceTime
		o.metrics.FrameCount++
	}()

	frameWidth := frame.Cols()
	frameHeight := frame.Rows()

	// 1. Preprocess frame: resize and normalize
	preprocessed, scale, padX, padY, err := PreprocessFrameForYOLOWithInfo(frame, o.inputWidth, o.inputHeight)
	if err != nil {
		return nil, fmt.Errorf("onnx detector: preprocess: %w", err)
	}
	defer preprocessed.Close()

	// Store letterbox padding info for post-processing
	o.letterboxScale = scale
	o.letterboxPadX = padX
	o.letterboxPadY = padY

	// 2. Convert Mat to float32 array for ONNX (NCHW format)
	// Preprocessed Mat is [height, width, channels] in RGB, normalized to [0,1]
	// ONNX expects [batch, channels, height, width] format (NCHW)

	// Get data from Mat
	// gocv Mat doesn't have DataPtrFloat32, we need to extract data differently
	// Convert Mat to a slice by accessing pixel data
	h, w := o.inputHeight, o.inputWidth
	inputData := o.inputTensor.GetData()

	// Convert HWC to NCHW: one CGO call to get all bytes, then pure-Go reorder
	hwcBytes := preprocessed.ToBytes()
	if want := h * w * 3 * 4; len(hwcBytes) != want {
		return nil, fmt.Errorf("onnx detector: preprocessed frame is %d bytes, want %d", len(hwcBytes), want)
	}
	hwcData := unsafe.Slice((*float32)(unsafe.Pointer(&hwcBytes[0])), h*w*3)
	for ch := 0; ch < 3; ch++ {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				inputData[ch*h*w+y*w+x] = hwcData[(y*w+x)*3+ch]
			}
		}
	}

	// 3. Run inference
	err = o.session.Run()
	if err != nil {
		return nil, fmt.Errorf("onnx detector: run inference: %w", err)
	}

	// 4. Extract output data
	outputData := o.outputTensor.GetData()

	// 5. Post-process: YOLOv11 format with letterbox correction
	boxes := YOLOv11PostProcessWithLetterbox(
		outputData,
		o.inputWidth, o.inputHeight,
		frameWidth, frameHeight,
		o.confidenceThreshold,
		o.nmsThreshold,
		o.letterboxScale,
		o.letterboxPadX,
		o.letterboxPadY,
	)

	return boxes, nil
}

// GetExecutionProvider returns the execution provider in use
func (o *OnnxDetector) GetExecutionProvider() string {
	return o.executionProvider
}

// GetInferenceTime returns the last inference time in milliseconds
func (o *OnnxDetector) GetInferenceTime() float32 {
	return o.inferenceTime
}

// SetNMSThreshold updates the NMS threshold used for post-processing
func (o *OnnxDetector) SetNMSThreshold(threshold float32) {
	o.nmsThreshold = threshold
}

// SetConfidenceThreshold updates the confidence threshold used for post-processing
func (o *OnnxDetector) SetConfidenceThreshold(threshold float32) {
	o.confidenceThreshold = threshold
}

// Close releases ONNX Runtime resources
func (o *OnnxDetector) Close() error {
	if o.inputTensor != nil {
		o.inputTensor.Destroy()
		o.inputTensor = nil
	}
	if o.outputTensor != nil {
		o.outputTensor.Destroy()
		o.outputTensor = nil
	}
	if o.session != nil {
		o.session.Destroy()
		o.session = nil
		o.sessionInitialized = false
	}
	log.Println("ONNX Detector closed")
	return nil
}

// resolveModelPath resolves model path from ONNXRUNTIME_HOME env var or absolute path
func resolveModelPath(modelPath string) (string, error) {
	// If absolute path, use as-is
	if filepath.IsAbs(modelPath) {
		return modelPath, nil
	}

	// Try ONNXRUNTIME_HOME environment variable
	onnxHome := os.Getenv("ONNXRUNTIME_HOME")
	if onnxHome != "" {
		resolved := filepath.Join(onnxHome, modelPath)
		if _, err := os.Stat(resolved); err == nil {
			return resolved, nil
		}
	}

	// Try current directory
	if _, err := os.Stat(modelPath); err == nil {
		abs, _ := filepath.Abs(modelPath)
		return abs, nil
	}

	return "", fmt.Errorf("could not resolve model path: %s", modelPath)
}

// FallbackDetector provides a CPU-only fallback implementation
// Useful for testing and when ONNX Runtime is unavailable
type FallbackDetector struct {
	inferenceTime float32
	metrics       *MetricsCollector
}

// NewFallbackDetector creates a fallback detector (CPU only, mock)
func NewFallbackDetector() *FallbackDetector {
	return &FallbackDetector{
		inferenceTime: 0,
		metrics:       NewMetricsCollector(),
	}
}

// DetectFaces is a mock implementation that returns no detections
func (f *FallbackDetector) DetectFaces(frame gocv.Mat) ([]BoundingBox, error) {
	if frame.Empty() {
		return nil, fmt.Errorf("fallback detector: empty frame")
	}

	start := time.Now()
	defer func() {
		f.inferenceTime = float32(time.Since(start).Milliseconds())
		f.metrics.InferenceTime = f.inferenceTime
		f.metrics.FrameCount++
	}()

	// Mock: no detections
	return []BoundingBox{}, nil
}

// GetExecutionProvider returns CPU
func (f *FallbackDetector) GetExecutionProvider() string {
	return "CPU (fallback mock)"
}

// GetInferenceTime returns inference time
func (f *FallbackDetector) GetInferenceTime() float32 {
	return f.inferenceTime
}

// Close is a no-op for fallback
func (f *FallbackDetector) Close() error {
	log.Println("Fallback Detector closed")
	return nil
}
