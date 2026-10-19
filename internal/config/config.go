package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// CameraSourceConfig represents a single camera source configuration
type CameraSourceConfig struct {
	Type     string `mapstructure:"type"`      // "local" or "rtsp"
	DeviceID int    `mapstructure:"device_id"` // For local cameras
	URL      string `mapstructure:"url"`       // For RTSP streams
	Width    int    `mapstructure:"width"`     // Optional: override resolution
	Height   int    `mapstructure:"height"`    // Optional: override resolution
	FPS      int    `mapstructure:"fps"`       // Optional: override FPS
	Format   string `mapstructure:"format"`    // Optional: camera format (for local cameras)
	Mirror   bool   `mapstructure:"mirror"`    // Optional: mirror/flip output horizontally
	// ManualExposure, if > 0, disables the camera's auto-exposure and locks
	// exposure_time_absolute to this value. Some cameras keep frame
	// brightness roughly constant regardless of true ambient light via an
	// ISP-level auto-brightness that isn't exposed through any v4l2
	// control; locking exposure manually is the fix when raw frame
	// brightness needs to track real ambient light. 0 = leave
	// auto-exposure enabled.
	ManualExposure int `mapstructure:"manual_exposure"`
}

// Config holds application-wide configuration
type Config struct {
	mu sync.RWMutex // Protects concurrent access during hot-reload

	// Camera sources (new multi-source support)
	CameraSources []CameraSourceConfig
	ActiveSource  int // Index of active camera source

	// Legacy single camera fields (for backward compatibility)
	CameraIndex int
	Width       int
	Height      int
	FPS         int    // Target FPS for capture and output
	Format      string // Camera format (FOURCC code, e.g., "YUYV", "MJPG", "H264")

	// Inference settings
	ConfidenceThreshold float32
	NMSThreshold        float32
	ModelPath           string
	InferenceWidth      int     // Model input width
	InferenceHeight     int     // Model input height
	DetectionFPS        float32 // Target detections per second (0 = every frame, scales with FPS)

	// Cameraman settings (stabilization)
	SmoothingFactor     float32 // EMA alpha, typically 0.1-0.3
	ZoomMin             float32
	ZoomMax             float32
	TargetFaceSizeMin   float32 // Target face size as percentage of frame (e.g., 0.25 = 25% of frame height)
	TargetFaceSizeMax   float32 // Maximum face size before zooming out (e.g., 0.50 = 50% of frame height)
	ZoomSmoothingFactor float32 // Smoothing factor for zoom adjustments (0.0-1.0, lower = smoother)
	FaceBoxPadding      float32 // Padding multiplier for face bounding box (e.g., 1.3 = 30% larger). Expands box outward from center

	// Logger
	LogLevel string

	// Virtual camera settings
	VirtualCamEnabled      bool
	VirtualCamDevice       string
	VirtualCamFormat       string
	VirtualCamOutputWidth  int
	VirtualCamOutputHeight int

	// API server for remote control and state queries
	APIEnabled bool
	APIPort    int

	// Capture control
	StartCapture bool // If true, start capture at launch; if false, start paused (toggle via API or SIGUSR1)

	// Other flags
	NoDetection bool

	// Debug/visualization
	ShowBoxes bool // Show detection and crop boxes on output

	// Background processing
	BackgroundEnabled      bool
	BackgroundMode         string  // "blur" or "replace"
	BackgroundBlurStrength int     // Gaussian blur kernel size (odd number, e.g., 15, 21, 31)
	BackgroundImagePath    string  // Path to background image for replacement
	BackgroundColor        string  // Background color for replacement (hex format, e.g., "#000000" or "black")
	BackgroundFacePadding  float32 // Padding around face for mask (e.g., 1.2 = 20% larger)
}

// New creates a Config with sensible defaults
func New() *Config {
	return &Config{
		// Camera sources (empty by default, will be populated from legacy fields if needed)
		CameraSources: []CameraSourceConfig{},
		ActiveSource:  0,

		// Legacy single camera fields
		CameraIndex:            0,
		Width:                  1280,
		Height:                 720,
		FPS:                    60,
		Format:                 "", // Empty = use camera default
		ConfidenceThreshold:    0.5,
		NMSThreshold:           0.4,
		ModelPath:              "",
		InferenceWidth:         640,
		InferenceHeight:        640,
		DetectionFPS:           5.0, // 5 detections per second (scales with FPS)
		SmoothingFactor:        0.05,
		ZoomMin:                1.0,
		ZoomMax:                1.5,
		TargetFaceSizeMin:      0.25, // Target face should be at least 25% of frame height
		TargetFaceSizeMax:      0.50, // If face exceeds 50% of frame height, zoom out
		ZoomSmoothingFactor:    0.1,  // Smooth zoom adjustments (lower = smoother)
		FaceBoxPadding:         1.3,  // Expand face box by 30% to account for tight detection boxes
		LogLevel:               "info",
		VirtualCamEnabled:      true,
		VirtualCamDevice:       "",
		VirtualCamFormat:       "YUYV",
		VirtualCamOutputWidth:  0,
		VirtualCamOutputHeight: 0,
		APIEnabled:             true,
		APIPort:                8090,
		StartCapture:           true, // Start capture at launch by default
		NoDetection:            false,
		ShowBoxes:              false,
		BackgroundEnabled:      false,
		BackgroundMode:         "blur",
		BackgroundBlurStrength: 21,
		BackgroundImagePath:    "",
		BackgroundColor:        "#000000", // Black
		BackgroundFacePadding:  1.3,       // 30% larger than face box
	}
}

// ResolvePath returns configPath if set, otherwise the first existing default
// location (./config.yaml, then ~/.config/virtual-cameraman/config.yaml), or "".
func ResolvePath(configPath string) string {
	if configPath != "" {
		return configPath
	}
	// Try current directory first
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml"
	}
	if homeDir, err := os.UserHomeDir(); err == nil {
		// Try ~/.config/virtual-cameraman/config.yaml
		defaultPath := filepath.Join(homeDir, ".config", "virtual-cameraman", "config.yaml")
		if _, err := os.Stat(defaultPath); err == nil {
			return defaultPath
		}
	}
	return ""
}

// LoadFromFile loads configuration from a YAML file.
// If configPath is empty, the default locations are tried (see ResolvePath).
// If the file doesn't exist, returns a default config (no error).
// If the file exists but is invalid, returns an error.
func LoadFromFile(configPath string) (*Config, error) {
	cfg := New()

	configPath = ResolvePath(configPath)

	// If still no config found, return defaults
	if configPath == "" {
		return cfg, nil
	}

	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return cfg, nil // File doesn't exist, return defaults
	}

	// Setup viper
	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")

	// Read config file
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	// Map YAML keys to config struct
	// Camera sources (new multi-source support)
	if v.IsSet("camera.sources") {
		var sources []CameraSourceConfig
		if err := v.UnmarshalKey("camera.sources", &sources); err == nil {
			cfg.CameraSources = sources
		}
	}
	if v.IsSet("camera.active_source") {
		cfg.ActiveSource = v.GetInt("camera.active_source")
	}

	// Camera settings (legacy single camera support)
	if v.IsSet("camera.index") {
		cfg.CameraIndex = v.GetInt("camera.index")
	}
	if v.IsSet("camera.width") {
		cfg.Width = v.GetInt("camera.width")
	}
	if v.IsSet("camera.height") {
		cfg.Height = v.GetInt("camera.height")
	}
	if v.IsSet("camera.fps") {
		cfg.FPS = v.GetInt("camera.fps")
	}
	if v.IsSet("camera.format") {
		cfg.Format = v.GetString("camera.format")
	}

	// Inference settings
	if v.IsSet("inference.confidence_threshold") {
		cfg.ConfidenceThreshold = float32(v.GetFloat64("inference.confidence_threshold"))
	}
	if v.IsSet("inference.nms_threshold") {
		cfg.NMSThreshold = float32(v.GetFloat64("inference.nms_threshold"))
	}
	if v.IsSet("inference.model_path") {
		cfg.ModelPath = v.GetString("inference.model_path")
	}
	if v.IsSet("inference.width") {
		cfg.InferenceWidth = v.GetInt("inference.width")
	}
	if v.IsSet("inference.height") {
		cfg.InferenceHeight = v.GetInt("inference.height")
	}
	if v.IsSet("inference.detection_fps") {
		cfg.DetectionFPS = float32(v.GetFloat64("inference.detection_fps"))
	}

	// Cameraman settings
	if v.IsSet("cameraman.smoothing_factor") {
		cfg.SmoothingFactor = float32(v.GetFloat64("cameraman.smoothing_factor"))
	}
	if v.IsSet("cameraman.zoom_min") {
		cfg.ZoomMin = float32(v.GetFloat64("cameraman.zoom_min"))
	}
	if v.IsSet("cameraman.zoom_max") {
		cfg.ZoomMax = float32(v.GetFloat64("cameraman.zoom_max"))
	}
	if v.IsSet("cameraman.target_face_size_min") {
		cfg.TargetFaceSizeMin = float32(v.GetFloat64("cameraman.target_face_size_min"))
	}
	if v.IsSet("cameraman.target_face_size_max") {
		cfg.TargetFaceSizeMax = float32(v.GetFloat64("cameraman.target_face_size_max"))
	}
	if v.IsSet("cameraman.zoom_smoothing_factor") {
		cfg.ZoomSmoothingFactor = float32(v.GetFloat64("cameraman.zoom_smoothing_factor"))
	}
	if v.IsSet("cameraman.face_box_padding") {
		cfg.FaceBoxPadding = float32(v.GetFloat64("cameraman.face_box_padding"))
	}

	// Logger
	if v.IsSet("log.level") {
		cfg.LogLevel = v.GetString("log.level")
	}

	// Virtual camera
	if v.IsSet("virtual_camera.enabled") {
		cfg.VirtualCamEnabled = v.GetBool("virtual_camera.enabled")
	}
	if v.IsSet("virtual_camera.device") {
		cfg.VirtualCamDevice = v.GetString("virtual_camera.device")
	}
	if v.IsSet("virtual_camera.format") {
		cfg.VirtualCamFormat = v.GetString("virtual_camera.format")
	}
	if v.IsSet("virtual_camera.output_width") {
		cfg.VirtualCamOutputWidth = v.GetInt("virtual_camera.output_width")
	}
	if v.IsSet("virtual_camera.output_height") {
		cfg.VirtualCamOutputHeight = v.GetInt("virtual_camera.output_height")
	}

	// API server
	if v.IsSet("api.enabled") {
		cfg.APIEnabled = v.GetBool("api.enabled")
	}
	if v.IsSet("api.port") {
		cfg.APIPort = v.GetInt("api.port")
	}

	// Capture control
	if v.IsSet("capture.start_at_launch") {
		cfg.StartCapture = v.GetBool("capture.start_at_launch")
	}

	// Other flags
	if v.IsSet("no_detection") {
		cfg.NoDetection = v.GetBool("no_detection")
	}

	// Debug/visualization
	if v.IsSet("debug.show_boxes") {
		cfg.ShowBoxes = v.GetBool("debug.show_boxes")
	}

	// Background processing
	if v.IsSet("background.enabled") {
		cfg.BackgroundEnabled = v.GetBool("background.enabled")
	}
	if v.IsSet("background.mode") {
		cfg.BackgroundMode = v.GetString("background.mode")
	}
	if v.IsSet("background.blur_strength") {
		cfg.BackgroundBlurStrength = v.GetInt("background.blur_strength")
	}
	if v.IsSet("background.image_path") {
		cfg.BackgroundImagePath = v.GetString("background.image_path")
	}
	if v.IsSet("background.color") {
		cfg.BackgroundColor = v.GetString("background.color")
	}
	if v.IsSet("background.face_padding") {
		cfg.BackgroundFacePadding = float32(v.GetFloat64("background.face_padding"))
	}

	return cfg, nil
}

// CalculateDetectionInterval calculates how many frames to skip between detections
// based on the actual camera FPS and desired detection FPS
// Returns: frames to skip (0 = every frame, 1 = every other frame, etc.)
func (c *Config) CalculateDetectionInterval(actualFPS int) int {
	if c.DetectionFPS <= 0 {
		return 0 // Detect every frame
	}
	if actualFPS <= 0 {
		return 1 // Safety fallback
	}

	// Calculate interval: if we want DetectionFPS detections per second
	// and camera runs at actualFPS, we detect every (actualFPS / DetectionFPS) frames
	interval := float32(actualFPS) / c.DetectionFPS

	// Round to nearest integer
	if interval < 1.0 {
		return 0 // Detect every frame
	}

	return int(interval)
}

// SaveToFile saves the current configuration to a YAML file, preserving existing settings
func (c *Config) SaveToFile(configPath string) error {
	// If no path provided, use default
	if configPath == "" {
		configPath = "config.yaml"
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(configPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}
	}

	// Setup viper for reading existing config first
	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")

	// Read existing config to preserve settings we don't modify
	// Ignore errors - file might not exist yet
	_ = v.ReadInConfig()

	// Now update only the fields we care about
	// Camera settings
	v.Set("camera.index", c.CameraIndex)
	v.Set("camera.width", c.Width)
	v.Set("camera.height", c.Height)
	v.Set("camera.fps", c.FPS)
	if c.Format != "" {
		v.Set("camera.format", c.Format)
	}

	// Camera sources (if using multi-source)
	if len(c.CameraSources) > 0 {
		v.Set("camera.sources", c.CameraSources)
		v.Set("camera.active_source", c.ActiveSource)
	}

	// Inference settings
	v.Set("inference.confidence_threshold", c.ConfidenceThreshold)
	v.Set("inference.nms_threshold", c.NMSThreshold)
	if c.ModelPath != "" {
		v.Set("inference.model_path", c.ModelPath)
	}
	v.Set("inference.width", c.InferenceWidth)
	v.Set("inference.height", c.InferenceHeight)
	v.Set("inference.detection_fps", c.DetectionFPS)

	// Cameraman settings
	v.Set("cameraman.smoothing_factor", c.SmoothingFactor)
	v.Set("cameraman.zoom_min", c.ZoomMin)
	v.Set("cameraman.zoom_max", c.ZoomMax)
	v.Set("cameraman.target_face_size_min", c.TargetFaceSizeMin)
	v.Set("cameraman.target_face_size_max", c.TargetFaceSizeMax)
	v.Set("cameraman.zoom_smoothing_factor", c.ZoomSmoothingFactor)
	v.Set("cameraman.face_box_padding", c.FaceBoxPadding)

	// Logger
	v.Set("log.level", c.LogLevel)

	// Virtual camera
	v.Set("virtual_camera.enabled", c.VirtualCamEnabled)
	if c.VirtualCamDevice != "" {
		v.Set("virtual_camera.device", c.VirtualCamDevice)
	}
	v.Set("virtual_camera.format", c.VirtualCamFormat)
	if c.VirtualCamOutputWidth > 0 {
		v.Set("virtual_camera.output_width", c.VirtualCamOutputWidth)
	}
	if c.VirtualCamOutputHeight > 0 {
		v.Set("virtual_camera.output_height", c.VirtualCamOutputHeight)
	}

	// API
	v.Set("api.enabled", c.APIEnabled)
	v.Set("api.port", c.APIPort)

	// Capture control
	v.Set("capture.start_at_launch", c.StartCapture)

	// Debug
	v.Set("debug.show_boxes", c.ShowBoxes)
	v.Set("no_detection", c.NoDetection)

	// Background processing
	v.Set("background.enabled", c.BackgroundEnabled)
	v.Set("background.mode", c.BackgroundMode)
	v.Set("background.blur_strength", c.BackgroundBlurStrength)
	if c.BackgroundImagePath != "" {
		v.Set("background.image_path", c.BackgroundImagePath)
	}
	v.Set("background.color", c.BackgroundColor)
	v.Set("background.face_padding", c.BackgroundFacePadding)

	// Write to file (this will merge with existing content)
	if err := v.WriteConfigAs(configPath); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// WatchConfig watches the config file for changes and calls onChange when modified
// Returns a function to stop watching
func WatchConfig(configPath string, onChange func(*Config)) (func(), error) {
	if configPath == "" {
		configPath = "config.yaml"
	}

	// Create watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	// Watch the parent directory rather than the file itself: many editors save
	// by writing a new file and renaming it over the old one, which would
	// silently drop a watch on the original inode.
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		watcher.Close()
		return nil, fmt.Errorf("failed to resolve config path: %w", err)
	}
	if err := watcher.Add(filepath.Dir(absPath)); err != nil {
		watcher.Close()
		return nil, fmt.Errorf("failed to watch config file: %w", err)
	}

	// Start watching in background
	stopChan := make(chan struct{})
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Reload when the config file is written or replaced
				if filepath.Clean(event.Name) != absPath || !(event.Op.Has(fsnotify.Write) || event.Op.Has(fsnotify.Create)) {
					continue
				}
				cfg, err := LoadFromFile(absPath)
				if err != nil {
					// Log error but continue watching
					fmt.Printf("Error reloading config: %v\n", err)
					continue
				}
				if onChange != nil {
					onChange(cfg)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				fmt.Printf("Config watcher error: %v\n", err)
			case <-stopChan:
				watcher.Close()
				return
			}
		}
	}()

	// Return stop function (sync.Once ensures close is safe even if watcher goroutine exits first)
	var once sync.Once
	return func() {
		once.Do(func() { close(stopChan) })
	}, nil
}

// View runs fn with a read lock held, for reading fields that hot-reload may change
func (c *Config) View(fn func(*Config)) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	fn(c)
}

// Update safely updates config values with mutex lock
func (c *Config) Update(fn func(*Config)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(c)
}
