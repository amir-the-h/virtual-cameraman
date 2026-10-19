package camera

import (
	"fmt"

	"gocv.io/x/gocv"
)

// LocalSource captures from a local webcam device
type LocalSource struct {
	deviceID int
	width    int
	height   int
	fps      int
	format   string
	webcam   *Webcam
}

// NewLocalSource creates a new local camera source
func NewLocalSource(deviceID, width, height, fps int, format string) *LocalSource {
	return &LocalSource{
		deviceID: deviceID,
		width:    width,
		height:   height,
		fps:      fps,
		format:   format,
	}
}

func (l *LocalSource) Open() error {
	var err error
	l.webcam, err = NewWebcam(l.deviceID, l.width, l.height, l.fps, l.format)
	if err != nil {
		return fmt.Errorf("open local camera %d: %w", l.deviceID, err)
	}
	return nil
}

func (l *LocalSource) Read() (gocv.Mat, error) {
	if l.webcam == nil {
		return gocv.NewMat(), fmt.Errorf("camera not opened")
	}
	return l.webcam.Read()
}

func (l *LocalSource) IsOpened() bool {
	return l.webcam != nil
}

func (l *LocalSource) Close() error {
	if l.webcam != nil {
		err := l.webcam.Close()
		l.webcam = nil
		return err
	}
	return nil
}

func (l *LocalSource) Name() string {
	return fmt.Sprintf("Local Camera %d", l.deviceID)
}

func (l *LocalSource) Width() int {
	if l.webcam != nil {
		return l.webcam.Width()
	}
	return l.width
}

func (l *LocalSource) Height() int {
	if l.webcam != nil {
		return l.webcam.Height()
	}
	return l.height
}

func (l *LocalSource) FPS() int {
	if l.webcam != nil {
		return l.webcam.FPS()
	}
	return l.fps
}
