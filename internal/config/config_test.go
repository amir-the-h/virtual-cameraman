package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	cfg := New()

	// Check defaults
	if cfg.CameraIndex != 0 {
		t.Errorf("CameraIndex = %v, want 0", cfg.CameraIndex)
	}
	if cfg.Width != 1280 {
		t.Errorf("Width = %v, want 1280", cfg.Width)
	}
	if cfg.Height != 720 {
		t.Errorf("Height = %v, want 720", cfg.Height)
	}
	if cfg.FPS != 60 {
		t.Errorf("FPS = %v, want 60", cfg.FPS)
	}
	if cfg.DetectionFPS != 5.0 {
		t.Errorf("DetectionFPS = %v, want 5.0", cfg.DetectionFPS)
	}
	if cfg.SmoothingFactor != 0.05 {
		t.Errorf("SmoothingFactor = %v, want 0.05", cfg.SmoothingFactor)
	}
	if cfg.VirtualCamEnabled != true {
		t.Errorf("VirtualCamEnabled = %v, want true", cfg.VirtualCamEnabled)
	}
	if cfg.StartCapture != true {
		t.Errorf("StartCapture = %v, want true", cfg.StartCapture)
	}
}

func TestLoadFromFile_NonExistent(t *testing.T) {
	cfg, err := LoadFromFile("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("LoadFromFile() should not return error for non-existent file, got: %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadFromFile() returned nil config")
	}
	// Should return defaults
	if cfg.CameraIndex != 0 {
		t.Errorf("Expected default CameraIndex, got %v", cfg.CameraIndex)
	}
}

func TestLoadFromFile_ValidYAML(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `camera:
  index: 1
  width: 1920
  height: 1080
  fps: 30
inference:
  confidence_threshold: 0.6
  detection_fps: 10.0
cameraman:
  smoothing_factor: 0.3
  zoom_min: 1.2
  zoom_max: 2.0
log:
  level: debug
virtual_camera:
  enabled: false
  device: "/dev/video3"
  format: "MJPEG"
debug:
  show_boxes: true
no_detection: false
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	cfg, err := LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile() returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadFromFile() returned nil config")
	}

	// Check loaded values
	if cfg.CameraIndex != 1 {
		t.Errorf("CameraIndex = %v, want 1", cfg.CameraIndex)
	}
	if cfg.Width != 1920 {
		t.Errorf("Width = %v, want 1920", cfg.Width)
	}
	if cfg.Height != 1080 {
		t.Errorf("Height = %v, want 1080", cfg.Height)
	}
	if cfg.FPS != 30 {
		t.Errorf("FPS = %v, want 30", cfg.FPS)
	}
	if cfg.ConfidenceThreshold != 0.6 {
		t.Errorf("ConfidenceThreshold = %v, want 0.6", cfg.ConfidenceThreshold)
	}
	if cfg.DetectionFPS != 10.0 {
		t.Errorf("DetectionFPS = %v, want 10.0", cfg.DetectionFPS)
	}
	if cfg.SmoothingFactor != 0.3 {
		t.Errorf("SmoothingFactor = %v, want 0.3", cfg.SmoothingFactor)
	}
	if cfg.ZoomMin != 1.2 {
		t.Errorf("ZoomMin = %v, want 1.2", cfg.ZoomMin)
	}
	if cfg.ZoomMax != 2.0 {
		t.Errorf("ZoomMax = %v, want 2.0", cfg.ZoomMax)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if cfg.VirtualCamEnabled != false {
		t.Errorf("VirtualCamEnabled = %v, want false", cfg.VirtualCamEnabled)
	}
	if cfg.VirtualCamDevice != "/dev/video3" {
		t.Errorf("VirtualCamDevice = %v, want /dev/video3", cfg.VirtualCamDevice)
	}
	if cfg.VirtualCamFormat != "MJPEG" {
		t.Errorf("VirtualCamFormat = %v, want MJPEG", cfg.VirtualCamFormat)
	}
	if cfg.ShowBoxes != true {
		t.Errorf("ShowBoxes = %v, want true", cfg.ShowBoxes)
	}
}

func TestLoadFromFile_InvalidYAML(t *testing.T) {
	// Create a temporary invalid config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `camera:
  index: invalid_value
  width: [invalid]
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	cfg, err := LoadFromFile(configPath)
	// Viper might not error on invalid values, but if it does, that's fine
	if err != nil {
		// Expected error for invalid YAML
		if cfg != nil {
			t.Error("LoadFromFile() returned both error and config")
		}
		return
	}
	// If no error, config should still be valid (viper might ignore invalid fields)
	if cfg == nil {
		t.Fatal("LoadFromFile() returned nil config")
	}
}

func TestCalculateDetectionInterval(t *testing.T) {
	tests := []struct {
		name         string
		detectionFPS float32
		actualFPS    int
		expected     int
	}{
		{
			name:         "every frame",
			detectionFPS: 60.0,
			actualFPS:    60,
			expected:     1, // Every frame (60/60 = 1, but we round)
		},
		{
			name:         "every other frame",
			detectionFPS: 30.0,
			actualFPS:    60,
			expected:     2, // Every 2 frames (60/30 = 2)
		},
		{
			name:         "every 12 frames",
			detectionFPS: 5.0,
			actualFPS:    60,
			expected:     12, // Every 12 frames (60/5 = 12)
		},
		{
			name:         "zero detection FPS",
			detectionFPS: 0.0,
			actualFPS:    60,
			expected:     0, // Detect every frame
		},
		{
			name:         "negative detection FPS",
			detectionFPS: -1.0,
			actualFPS:    60,
			expected:     0, // Detect every frame
		},
		{
			name:         "zero actual FPS",
			detectionFPS: 5.0,
			actualFPS:    0,
			expected:     1, // Safety fallback
		},
		{
			name:         "high detection rate",
			detectionFPS: 120.0,
			actualFPS:    60,
			expected:     0, // Detect every frame (interval < 1.0)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := New()
			cfg.DetectionFPS = tt.detectionFPS
			result := cfg.CalculateDetectionInterval(tt.actualFPS)
			if result != tt.expected {
				t.Errorf("CalculateDetectionInterval(%.1f, %d) = %d, want %d",
					tt.detectionFPS, tt.actualFPS, result, tt.expected)
			}
		})
	}
}

func TestWatchConfig_ReloadsOnAtomicRename(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("cameraman:\n  zoom_max: 1.5\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	reloaded := make(chan *Config, 4)
	stop, err := WatchConfig(configPath, func(c *Config) { reloaded <- c })
	if err != nil {
		t.Fatalf("WatchConfig() error: %v", err)
	}
	defer stop()

	// Save the way editors like vim do: write a temp file, rename it over the original
	tmp := filepath.Join(tmpDir, ".config.yaml.swp")
	if err := os.WriteFile(tmp, []byte("cameraman:\n  zoom_max: 2.5\n"), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	if err := os.Rename(tmp, configPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	select {
	case c := <-reloaded:
		if c.ZoomMax != 2.5 {
			t.Errorf("ZoomMax = %v, want 2.5", c.ZoomMax)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("config was not reloaded after rename")
	}
}

// Guards against config.yaml.example drifting from what the loader accepts
func TestLoadFromFile_ExampleConfig(t *testing.T) {
	cfg, err := LoadFromFile(filepath.Join("..", "..", "config.yaml.example"))
	if err != nil {
		t.Fatalf("config.yaml.example failed to load: %v", err)
	}
	if len(cfg.CameraSources) != 1 || cfg.CameraSources[0].Type != "local" {
		t.Errorf("CameraSources = %+v, want one local source", cfg.CameraSources)
	}
}
