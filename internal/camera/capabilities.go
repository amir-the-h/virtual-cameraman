package camera

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Resolution represents a camera resolution
type Resolution struct {
	Width  int
	Height int
}

// Format represents a camera format
type Format struct {
	Name        string
	Description string
	FourCC      string
}

// Capability represents camera capabilities for a specific resolution
type Capability struct {
	Resolution Resolution
	FPS        []int // Available FPS for this resolution
	Formats    []Format
}

// CameraCapabilities holds all capabilities for a camera
type CameraCapabilities struct {
	DeviceIndex int
	DevicePath  string
	Resolutions []Capability
	AllFormats  []Format
}

// GetCameraCapabilities queries camera capabilities using v4l2-ctl --list-formats-ext
func GetCameraCapabilities(deviceIndex int) (*CameraCapabilities, error) {
	devicePath := fmt.Sprintf("/dev/video%d", deviceIndex)

	// Check if device exists
	if _, err := exec.LookPath("v4l2-ctl"); err != nil {
		return nil, fmt.Errorf("v4l2-ctl not found: %w (install with: sudo apt install v4l-utils)", err)
	}

	capabilities := &CameraCapabilities{
		DeviceIndex: deviceIndex,
		DevicePath:  devicePath,
		Resolutions: []Capability{},
		AllFormats:  []Format{},
	}

	// Use --list-formats-ext to get everything in one call
	cmd := exec.Command("v4l2-ctl", "--device", devicePath, "--list-formats-ext")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("v4l2-ctl list-formats-ext: %w", err)
	}

	// Parse the nested structure
	formats, resolutions := parseFormatsExt(string(output))
	capabilities.AllFormats = formats

	// Build resolution map (resolution -> formats and FPS)
	resMap := make(map[string]*Capability)

	for formatIdx, format := range formats {
		if formatIdx < len(resolutions) {
			for _, resInfo := range resolutions[formatIdx] {
				key := fmt.Sprintf("%dx%d", resInfo.Width, resInfo.Height)

				if cap, exists := resMap[key]; exists {
					// Add format if not already present
					formatExists := false
					for _, f := range cap.Formats {
						if f.FourCC == format.FourCC {
							formatExists = true
							break
						}
					}
					if !formatExists {
						cap.Formats = append(cap.Formats, format)
					}
					// Merge FPS
					cap.FPS = mergeFPS(cap.FPS, resInfo.FPS)
				} else {
					resMap[key] = &Capability{
						Resolution: Resolution{Width: resInfo.Width, Height: resInfo.Height},
						FPS:        resInfo.FPS,
						Formats:    []Format{format},
					}
				}
			}
		}
	}

	// Convert map to slice
	for _, cap := range resMap {
		capabilities.Resolutions = append(capabilities.Resolutions, *cap)
	}

	return capabilities, nil
}

// resInfo holds resolution and FPS info during parsing
type resInfo struct {
	Width  int
	Height int
	FPS    []int
}

// parseFormatsExt parses --list-formats-ext output
func parseFormatsExt(output string) ([]Format, [][]resInfo) {
	var formats []Format
	var allResolutions [][]resInfo

	lines := strings.Split(output, "\n")
	var currentFormat *Format
	var currentResolutions []resInfo
	var currentSize *resInfo

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Format line: [0]: 'YUYV' (YUYV 4:2:2)
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]:") {
			// Save previous format's resolutions
			if currentFormat != nil {
				formats = append(formats, *currentFormat)
				allResolutions = append(allResolutions, currentResolutions)
				currentResolutions = nil
			}

			// Parse format
			parts := strings.SplitN(line, "]:", 2)
			if len(parts) == 2 {
				formatPart := strings.TrimSpace(parts[1])
				// Extract fourcc from quotes
				start := strings.Index(formatPart, "'")
				if start != -1 {
					end := strings.Index(formatPart[start+1:], "'")
					if end != -1 {
						fourcc := formatPart[start+1 : start+1+end]

						// Extract description
						desc := ""
						descStart := strings.Index(formatPart, "(")
						if descStart != -1 {
							descEnd := strings.Index(formatPart[descStart:], ")")
							if descEnd != -1 {
								desc = formatPart[descStart+1 : descStart+descEnd]
							}
						}

						currentFormat = &Format{
							Name:        fourcc,
							Description: desc,
							FourCC:      fourcc,
						}
					}
				}
			}
			currentSize = nil
		} else if strings.Contains(line, "Size: Discrete") {
			// Size line: Size: Discrete 1920x1080 (may have leading tabs)
			if currentSize != nil {
				// Save previous size
				currentResolutions = append(currentResolutions, *currentSize)
			}

			// Extract dimensions
			parts := strings.Fields(line)
			for _, part := range parts {
				if strings.Contains(part, "x") {
					dims := strings.Split(part, "x")
					if len(dims) == 2 {
						if width, err1 := strconv.Atoi(dims[0]); err1 == nil {
							if height, err2 := strconv.Atoi(dims[1]); err2 == nil {
								currentSize = &resInfo{
									Width:  width,
									Height: height,
									FPS:    []int{},
								}
								break
							}
						}
					}
				}
			}
		} else if strings.Contains(line, "Interval: Discrete") && currentSize != nil {
			// Interval line: Interval: Discrete 0.017s (60.000 fps) (may have leading tabs)
			parts := strings.Fields(line)
			for _, part := range parts {
				if strings.Contains(part, "fps") {
					// Extract FPS number
					fpsStr := strings.Trim(part, "fps)")
					fpsStr = strings.Trim(fpsStr, "(")
					if fps, err := strconv.ParseFloat(fpsStr, 64); err == nil {
						fpsInt := int(fps)
						// Check if already in list
						found := false
						for _, existing := range currentSize.FPS {
							if existing == fpsInt {
								found = true
								break
							}
						}
						if !found {
							currentSize.FPS = append(currentSize.FPS, fpsInt)
						}
					}
				}
			}
		}
	}

	// Save last format
	if currentFormat != nil {
		if currentSize != nil {
			currentResolutions = append(currentResolutions, *currentSize)
		}
		formats = append(formats, *currentFormat)
		allResolutions = append(allResolutions, currentResolutions)
	}

	return formats, allResolutions
}

// mergeFPS merges two FPS lists, removing duplicates
func mergeFPS(list1, list2 []int) []int {
	fpsMap := make(map[int]bool)
	for _, fps := range list1 {
		fpsMap[fps] = true
	}
	for _, fps := range list2 {
		fpsMap[fps] = true
	}

	var merged []int
	for fps := range fpsMap {
		merged = append(merged, fps)
	}

	// Sort
	for i := 0; i < len(merged)-1; i++ {
		for j := i + 1; j < len(merged); j++ {
			if merged[i] > merged[j] {
				merged[i], merged[j] = merged[j], merged[i]
			}
		}
	}

	return merged
}

// ListCapabilities lists all capabilities for a camera in a readable format
func (cc *CameraCapabilities) ListCapabilities() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Camera %d (%s) Capabilities:\n\n", cc.DeviceIndex, cc.DevicePath))

	// Group by format (like v4l2-ctl output)
	for formatIdx, format := range cc.AllFormats {
		sb.WriteString(fmt.Sprintf("[%d]: '%s' (%s)\n", formatIdx, format.FourCC, format.Description))

		// Find all resolutions for this format
		for _, cap := range cc.Resolutions {
			// Check if this resolution supports this format
			formatSupported := false
			for _, f := range cap.Formats {
				if f.FourCC == format.FourCC {
					formatSupported = true
					break
				}
			}

			if formatSupported {
				sb.WriteString(fmt.Sprintf("\tSize: Discrete %dx%d\n", cap.Resolution.Width, cap.Resolution.Height))

				// Sort FPS descending
				fpsSorted := make([]int, len(cap.FPS))
				copy(fpsSorted, cap.FPS)
				for i := 0; i < len(fpsSorted)-1; i++ {
					for j := i + 1; j < len(fpsSorted); j++ {
						if fpsSorted[i] < fpsSorted[j] {
							fpsSorted[i], fpsSorted[j] = fpsSorted[j], fpsSorted[i]
						}
					}
				}

				for _, fps := range fpsSorted {
					interval := 1.0 / float64(fps)
					sb.WriteString(fmt.Sprintf("\t\tInterval: Discrete %.3fs (%.3f fps)\n", interval, float64(fps)))
				}
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// FindBestResolution finds the best matching resolution
func (cc *CameraCapabilities) FindBestResolution(requestedWidth, requestedHeight int) (Resolution, []int) {
	var best Resolution
	var bestFPS []int
	bestDiff := -1

	for _, cap := range cc.Resolutions {
		diff := abs(cap.Resolution.Width-requestedWidth) + abs(cap.Resolution.Height-requestedHeight)
		if bestDiff == -1 || diff < bestDiff {
			bestDiff = diff
			best = cap.Resolution
			bestFPS = cap.FPS
		}
	}

	return best, bestFPS
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// GetBestFPS finds the best matching FPS from available FPS list
func GetBestFPS(availableFPS []int, requestedFPS int) int {
	if len(availableFPS) == 0 {
		return requestedFPS
	}

	// Find closest match
	best := availableFPS[0]
	bestDiff := abs(best - requestedFPS)

	for _, fps := range availableFPS {
		diff := abs(fps - requestedFPS)
		if diff < bestDiff {
			bestDiff = diff
			best = fps
		}
	}

	return best
}
