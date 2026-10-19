package camera

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// exposureControlNames lists the v4l2 control names cameras use for manual
// exposure time, across driver naming variants.
var manualExposureControlNames = []string{"exposure_time_absolute", "exposure_absolute"}

// SetManualExposure disables a local camera's auto-exposure and locks it to
// a fixed exposure time. Some webcams keep frame brightness roughly constant
// regardless of true ambient light via an ISP-level auto-brightness that
// isn't reflected in any readable v4l2 control (auto_exposure and
// exposure_time_absolute readback simply stay pinned at a fixed value).
// Forcing manual mode with a fixed exposure makes frame brightness track
// real ambient light again. Only applied when a camera source sets
// manual_exposure > 0.
func SetManualExposure(deviceIndex int, exposureValue int) error {
	devicePath := fmt.Sprintf("/dev/video%d", deviceIndex)
	if _, err := exec.LookPath("v4l2-ctl"); err != nil {
		return fmt.Errorf("v4l2-ctl not found: %w", err)
	}

	if out, err := exec.Command("v4l2-ctl", "-d", devicePath, "--set-ctrl=auto_exposure=1").CombinedOutput(); err != nil {
		return fmt.Errorf("set auto_exposure=1 (manual mode): %w (%s)", err, strings.TrimSpace(string(out)))
	}

	var lastErr error
	for _, ctrlName := range manualExposureControlNames {
		out, err := exec.Command("v4l2-ctl", "-d", devicePath, fmt.Sprintf("--set-ctrl=%s=%d", ctrlName, exposureValue)).CombinedOutput()
		if err == nil {
			log.Printf("Camera %s: manual exposure set (%s=%d)", devicePath, ctrlName, exposureValue)
			return nil
		}
		lastErr = fmt.Errorf("%s: %w (%s)", ctrlName, err, strings.TrimSpace(string(out)))
	}
	return fmt.Errorf("failed to set exposure control on %s: %w", devicePath, lastErr)
}
