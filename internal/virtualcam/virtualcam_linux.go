//go:build linux
// +build linux

package virtualcam

import (
	"fmt"
	"image"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"gocv.io/x/gocv"
)

// v4l2 structures (from linux/videodev2.h)
const (
	V4L2_BUF_TYPE_VIDEO_CAPTURE = 1
	V4L2_BUF_TYPE_VIDEO_OUTPUT  = 2
	V4L2_FIELD_ANY              = 0
	V4L2_PIX_FMT_YUYV           = 0x56595559 // 'YUYV'
	V4L2_PIX_FMT_MJPEG          = 0x47504A4D // 'MJPG'
	V4L2_PIX_FMT_BGR32          = 0x34524742 // 'BGR4'
	V4L2_CAP_VIDEO_OUTPUT       = 0x00000002
	VIDIOC_S_FMT                = 0xc0cc5605
	VIDIOC_G_FMT                = 0xc0cc5604
	VIDIOC_QUERYCAP             = 0x80685600
	VIDIOC_STREAMON             = 0x40045612
	VIDIOC_STREAMOFF            = 0x40045613
)

type v4l2_capability struct {
	driver       [16]uint8
	card         [32]uint8
	bus_info     [32]uint8
	version      uint32
	capabilities uint32
	device_caps  uint32
	reserved     [3]uint32
}

type v4l2_pix_format struct {
	width        uint32
	height       uint32
	pixelformat  uint32
	field        uint32
	bytesperline uint32
	sizeimage    uint32
	colorspace   uint32
	priv         uint32
	flags        uint32
	quantization uint32
	xfer_func    uint32
}

type v4l2_format struct {
	type_ uint32
	fmt   [200]byte // union, we'll use pix format
	_     [200]byte // padding for 64-bit alignment
}

// LinuxVirtualCamera implements VirtualCamera for Linux using v4l2loopback
type LinuxVirtualCamera struct {
	devicePath   string
	fd           int
	width        int
	height       int
	pixelFormat  uint32 // V4L2_PIX_FMT_YUYV, V4L2_PIX_FMT_MJPEG, or V4L2_PIX_FMT_BGR32
	bytesPerLine int
	open         bool
	yuyvBuf      []byte // Pre-allocated YUYV conversion buffer
	bgrPaddedBuf []byte // Pre-allocated BGR32 padded buffer
}

// NewLinuxVirtualCamera creates a new Linux virtual camera
// devicePath: path to v4l2loopback device (e.g., "/dev/video2")
// width, height: output resolution
// pixelFormat: "YUYV", "MJPEG", or "BGR4" (defaults to YUYV)
func NewLinuxVirtualCamera(devicePath string, width, height int, pixelFormat string) (*LinuxVirtualCamera, error) {
	vc := &LinuxVirtualCamera{
		devicePath: devicePath,
		width:      width,
		height:     height,
	}

	// Parse pixel format
	switch pixelFormat {
	case "YUYV", "yuyv":
		vc.pixelFormat = V4L2_PIX_FMT_YUYV
	case "MJPEG", "mjpeg", "MJPG", "mjpg":
		vc.pixelFormat = V4L2_PIX_FMT_MJPEG
	case "BGR4", "bgr4", "BGR32", "bgr32", "BGRA", "bgra", "BGRX", "bgrx":
		vc.pixelFormat = V4L2_PIX_FMT_BGR32
	default:
		fmt.Printf("Warning: unrecognized pixel format %q, defaulting to YUYV. Valid values: YUYV, MJPEG, BGR4\n", pixelFormat)
		vc.pixelFormat = V4L2_PIX_FMT_YUYV
	}

	// Open device - try without O_NONBLOCK first for format operations
	// Some v4l2loopback versions need blocking mode for format setting
	fd, err := syscall.Open(devicePath, syscall.O_RDWR, 0)
	if err != nil {
		// Fallback to non-blocking if blocking fails
		fd, err = syscall.Open(devicePath, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to open virtual camera device %s: %w", devicePath, err)
		}
	}
	vc.fd = fd

	// Query capabilities
	var cap v4l2_capability
	err = ioctl(vc.fd, VIDIOC_QUERYCAP, uintptr(unsafe.Pointer(&cap)))
	if err != nil {
		syscall.Close(vc.fd)
		return nil, fmt.Errorf("failed to query capabilities: %w", err)
	}

	// Check if device supports video output
	caps := cap.capabilities
	if cap.device_caps != 0 {
		caps = cap.device_caps
	}
	if caps&V4L2_CAP_VIDEO_OUTPUT == 0 {
		syscall.Close(vc.fd)
		return nil, fmt.Errorf("device %s does not support video output (capabilities: 0x%x). Is this a v4l2loopback device?", devicePath, caps)
	}

	// Set format - try direct ioctl first, then fallback to v4l2-ctl
	err = vc.setFormat()
	if err != nil {
		// Try using v4l2-ctl as fallback (more reliable for some v4l2loopback versions)
		fmt.Printf("Direct format setting failed: %v. Trying v4l2-ctl...\n", err)
		err2 := vc.setFormatViaV4L2Ctl()
		if err2 != nil {
			// Log warning but continue - v4l2loopback might auto-detect format from first frame
			fmt.Printf("Warning: failed to set format via v4l2-ctl: %v. Will try to auto-detect from first frame.\n", err2)
			vc.syncFormatFromDevice()
			// Don't return error - we'll try writing frames anyway
		} else {
			fmt.Printf("Format set successfully via v4l2-ctl\n")
		}
	}

	// Note: v4l2loopback doesn't always require VIDIOC_STREAMON
	// Some implementations work without it, but we'll try it anyway
	// If it fails, we'll continue (v4l2loopback might work without streaming)
	bufType := V4L2_BUF_TYPE_VIDEO_OUTPUT
	streamErr := ioctl(vc.fd, VIDIOC_STREAMON, uintptr(unsafe.Pointer(&bufType)))
	if streamErr != nil {
		// Log but don't fail - v4l2loopback might work without STREAMON
		// Some versions of v4l2loopback don't require this
	}

	vc.open = true
	return vc, nil
}

// setFormat sets the video format for the virtual camera
func (vc *LinuxVirtualCamera) setFormat() error {
	// First, query the current format to get the structure properly initialized
	var format v4l2_format
	format.type_ = V4L2_BUF_TYPE_VIDEO_OUTPUT

	// Query current format first (some drivers need this)
	err := ioctl(vc.fd, VIDIOC_G_FMT, uintptr(unsafe.Pointer(&format)))
	if err != nil {
		// If query fails, try setting format directly
		// This might work for v4l2loopback
	} else {
		// If query succeeded, the format structure is now initialized
		// We can modify it and set it
	}

	// Access the pix format through the union
	pix := (*v4l2_pix_format)(unsafe.Pointer(&format.fmt[0]))
	pix.width = uint32(vc.width)
	pix.height = uint32(vc.height)
	pix.pixelformat = vc.pixelFormat
	pix.field = V4L2_FIELD_ANY

	// Calculate bytes per line and size
	if vc.pixelFormat == V4L2_PIX_FMT_YUYV {
		// YUYV: 2 bytes per pixel
		pix.bytesperline = uint32(vc.width * 2)
		pix.sizeimage = uint32(vc.width * vc.height * 2)
	} else if vc.pixelFormat == V4L2_PIX_FMT_MJPEG {
		// MJPEG: compressed, estimate size (worst case: uncompressed)
		pix.bytesperline = 0                             // Not applicable for compressed
		pix.sizeimage = uint32(vc.width * vc.height * 3) // Estimate
	} else if vc.pixelFormat == V4L2_PIX_FMT_BGR32 {
		// BGR4: 4 bytes per pixel
		pix.bytesperline = uint32(vc.width * 4)
		pix.sizeimage = uint32(vc.width * vc.height * 4)
	}

	// Set the format
	err = ioctl(vc.fd, VIDIOC_S_FMT, uintptr(unsafe.Pointer(&format)))
	if err != nil {
		return fmt.Errorf("VIDIOC_S_FMT failed: %w", err)
	}

	// Verify what was actually set
	actualPix := (*v4l2_pix_format)(unsafe.Pointer(&format.fmt[0]))

	expectedStride := vc.width * 2
	expectedSize := vc.width * vc.height * 2
	switch vc.pixelFormat {
	case V4L2_PIX_FMT_BGR32:
		expectedStride = vc.width * 4
		expectedSize = vc.width * vc.height * 4
	case V4L2_PIX_FMT_MJPEG:
		expectedStride = 0
		expectedSize = vc.width * vc.height * 3
	}

	fmt.Printf("Format negotiation:\n")
	fmt.Printf("  Requested: %dx%d, format=0x%x, stride=%d, sizeimage=%d\n",
		vc.width, vc.height, vc.pixelFormat, expectedStride, expectedSize)
	fmt.Printf("  Accepted:  %dx%d, format=0x%x, stride=%d, sizeimage=%d\n",
		actualPix.width, actualPix.height, actualPix.pixelformat,
		actualPix.bytesperline, actualPix.sizeimage)

	if actualPix.width != uint32(vc.width) || actualPix.height != uint32(vc.height) {
		fmt.Printf("Warning: requested %dx%d, got %dx%d. Using device dimensions.\n",
			vc.width, vc.height, actualPix.width, actualPix.height)
		vc.width = int(actualPix.width)
		vc.height = int(actualPix.height)
	}

	if actualPix.pixelformat != vc.pixelFormat {
		switch actualPix.pixelformat {
		case V4L2_PIX_FMT_YUYV, V4L2_PIX_FMT_MJPEG, V4L2_PIX_FMT_BGR32:
			fmt.Printf("Warning: requested format 0x%x, got 0x%x. Using device format.\n",
				vc.pixelFormat, actualPix.pixelformat)
			vc.pixelFormat = actualPix.pixelformat
		default:
			return fmt.Errorf("unsupported pixel format: 0x%x", actualPix.pixelformat)
		}
	}

	switch vc.pixelFormat {
	case V4L2_PIX_FMT_YUYV:
		expectedStride := uint32(vc.width * 2)
		if actualPix.bytesperline != 0 && actualPix.bytesperline != expectedStride {
			fmt.Printf("Warning: stride mismatch: requested %d, got %d. Will pad lines.\n",
				expectedStride, actualPix.bytesperline)
		}
		if actualPix.bytesperline > 0 {
			vc.bytesPerLine = int(actualPix.bytesperline)
		} else {
			vc.bytesPerLine = int(expectedStride)
		}
	case V4L2_PIX_FMT_BGR32:
		expectedStride := uint32(vc.width * 4)
		if actualPix.bytesperline != 0 && actualPix.bytesperline != expectedStride {
			fmt.Printf("Warning: stride mismatch: requested %d, got %d. Will pad lines.\n",
				expectedStride, actualPix.bytesperline)
		}
		if actualPix.bytesperline > 0 {
			vc.bytesPerLine = int(actualPix.bytesperline)
		} else {
			vc.bytesPerLine = int(expectedStride)
		}
	}

	return nil
}

// syncFormatFromDevice refreshes format settings from the device.
func (vc *LinuxVirtualCamera) syncFormatFromDevice() {
	var format v4l2_format
	format.type_ = V4L2_BUF_TYPE_VIDEO_OUTPUT
	if err := ioctl(vc.fd, VIDIOC_G_FMT, uintptr(unsafe.Pointer(&format))); err != nil {
		format.type_ = V4L2_BUF_TYPE_VIDEO_CAPTURE
		if err := ioctl(vc.fd, VIDIOC_G_FMT, uintptr(unsafe.Pointer(&format))); err != nil {
			fmt.Printf("Warning: failed to query device format: %v\n", err)
			return
		}
	}

	pix := (*v4l2_pix_format)(unsafe.Pointer(&format.fmt[0]))
	if pix.width > 0 && pix.height > 0 {
		vc.width = int(pix.width)
		vc.height = int(pix.height)
	}

	switch pix.pixelformat {
	case V4L2_PIX_FMT_YUYV, V4L2_PIX_FMT_MJPEG, V4L2_PIX_FMT_BGR32:
		vc.pixelFormat = pix.pixelformat
	default:
		fmt.Printf("Warning: device reported unsupported format 0x%x\n", pix.pixelformat)
	}

	switch vc.pixelFormat {
	case V4L2_PIX_FMT_YUYV:
		if pix.bytesperline > 0 {
			vc.bytesPerLine = int(pix.bytesperline)
		} else {
			vc.bytesPerLine = vc.width * 2
		}
	case V4L2_PIX_FMT_BGR32:
		if pix.bytesperline > 0 {
			vc.bytesPerLine = int(pix.bytesperline)
		} else {
			vc.bytesPerLine = vc.width * 4
		}
	}
}

// setFormatViaV4L2Ctl sets the format using v4l2-ctl command (fallback method)
func (vc *LinuxVirtualCamera) setFormatViaV4L2Ctl() error {
	// Check if v4l2-ctl is available
	if _, err := exec.LookPath("v4l2-ctl"); err != nil {
		return fmt.Errorf("v4l2-ctl not found")
	}

	// Build format string
	var formatStr string
	if vc.pixelFormat == V4L2_PIX_FMT_YUYV {
		formatStr = "YUYV"
	} else if vc.pixelFormat == V4L2_PIX_FMT_MJPEG {
		formatStr = "MJPG"
	} else if vc.pixelFormat == V4L2_PIX_FMT_BGR32 {
		formatStr = "BGR4"
	} else {
		return fmt.Errorf("unsupported pixel format")
	}

	// Set format using v4l2-ctl
	commandArgs := [][]string{
		{"--device", vc.devicePath, "--set-fmt-video-out", fmt.Sprintf("pixelformat=%s,width=%d,height=%d", formatStr, vc.width, vc.height)},
		{"--device", vc.devicePath, "--set-fmt-video", fmt.Sprintf("pixelformat=%s,width=%d,height=%d", formatStr, vc.width, vc.height)},
	}

	var lastErr error
	for _, args := range commandArgs {
		cmd := exec.Command("v4l2-ctl", args...)
		output, err := cmd.CombinedOutput()
		if err == nil {
			vc.syncFormatFromDevice()
			return nil
		}
		lastErr = fmt.Errorf("v4l2-ctl failed: %v (output: %s)", err, string(output))
	}

	return lastErr
}

// WriteFrame writes a frame to the virtual camera
// Frame should be in BGR format (OpenCV default)
func (vc *LinuxVirtualCamera) WriteFrame(frame gocv.Mat) error {
	if !vc.open {
		return fmt.Errorf("virtual camera is not open")
	}

	if frame.Empty() {
		return fmt.Errorf("empty frame")
	}

	// Convert frame to the required format
	var outputFrame gocv.Mat
	var err error

	if vc.pixelFormat == V4L2_PIX_FMT_YUYV {
		// Convert BGR to YUYV - get raw bytes directly
		data, err := vc.convertToYUYVBytes(frame)
		if err != nil {
			return fmt.Errorf("failed to convert to YUYV: %w", err)
		}

		// Debug: log first write details
		expectedSize := vc.width * vc.height * 2
		if vc.bytesPerLine > 0 {
			expectedSize = vc.bytesPerLine * vc.height
		}
		if len(data) != expectedSize {
			return fmt.Errorf("YUYV data size mismatch: got %d bytes, expected %d (%dx%d, stride=%d)",
				len(data), expectedSize, vc.width, vc.height, vc.bytesPerLine)
		}

		// Debug first frame
		if false { // Enable for debugging
			fmt.Printf("YUYV frame: %dx%d, %d bytes\n", vc.width, vc.height, len(data))
			fmt.Printf("First 16 bytes: ")
			for i := 0; i < 16 && i < len(data); i++ {
				fmt.Printf("%02x ", data[i])
			}
			fmt.Println()
		}

		// Write to device
		written := 0
		retries := 0
		const maxRetries = 200
		for written < len(data) {
			n, err := syscall.Write(vc.fd, data[written:])
			if err != nil {
				if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
					retries++
					if retries > maxRetries {
						return fmt.Errorf("virtual camera write timed out: no consumer reading")
					}
					time.Sleep(500 * time.Microsecond)
					continue
				}
				return fmt.Errorf("failed to write frame: %w", err)
			}
			retries = 0
			written += n
		}

		return nil
	} else if vc.pixelFormat == V4L2_PIX_FMT_BGR32 {
		data, err := vc.convertToBGR32Bytes(frame)
		if err != nil {
			return fmt.Errorf("failed to convert to BGR32: %w", err)
		}

		expectedSize := vc.width * vc.height * 4
		if vc.bytesPerLine > 0 {
			expectedSize = vc.bytesPerLine * vc.height
		}
		if len(data) != expectedSize {
			return fmt.Errorf("BGR32 data size mismatch: got %d bytes, expected %d (%dx%d, stride=%d)",
				len(data), expectedSize, vc.width, vc.height, vc.bytesPerLine)
		}

		written := 0
		retries := 0
		const maxRetries = 200
		for written < len(data) {
			n, err := syscall.Write(vc.fd, data[written:])
			if err != nil {
				if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
					retries++
					if retries > maxRetries {
						return fmt.Errorf("virtual camera write timed out: no consumer reading")
					}
					time.Sleep(500 * time.Microsecond)
					continue
				}
				return fmt.Errorf("failed to write frame: %w", err)
			}
			retries = 0
			written += n
		}

		return nil
	} else if vc.pixelFormat == V4L2_PIX_FMT_MJPEG {
		// Encode to MJPEG
		outputFrame, err = vc.encodeToMJPEG(frame)
		if err != nil {
			return fmt.Errorf("failed to encode to MJPEG: %w", err)
		}
		defer outputFrame.Close()
	} else {
		return fmt.Errorf("unsupported pixel format: 0x%x", vc.pixelFormat)
	}

	// Validate frame immediately after conversion
	if outputFrame.Empty() {
		return fmt.Errorf("output frame is empty after conversion (format: 0x%x)", vc.pixelFormat)
	}

	// Additional validation - check dimensions
	if outputFrame.Cols() <= 0 || outputFrame.Rows() <= 0 {
		return fmt.Errorf("output frame has invalid dimensions: %dx%d", outputFrame.Cols(), outputFrame.Rows())
	}

	// Get frame data
	data := outputFrame.ToBytes()
	if len(data) == 0 {
		return fmt.Errorf("failed to get frame data (frame is %dx%d, type: %d)",
			outputFrame.Cols(), outputFrame.Rows(), outputFrame.Type())
	}

	// Write to device
	written := 0
	retries := 0
	const maxRetries = 200
	for written < len(data) {
		n, err := syscall.Write(vc.fd, data[written:])
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				retries++
				if retries > maxRetries {
					return fmt.Errorf("virtual camera write timed out: no consumer reading")
				}
				time.Sleep(500 * time.Microsecond)
				continue
			}
			return fmt.Errorf("failed to write frame: %w", err)
		}
		retries = 0
		written += n
	}

	return nil
}

// convertToBGR32Bytes converts a BGR frame to BGR4 (BGRA) format and returns raw bytes.
func (vc *LinuxVirtualCamera) convertToBGR32Bytes(frame gocv.Mat) ([]byte, error) {
	// Resize if needed
	var resized gocv.Mat
	if frame.Cols() != vc.width || frame.Rows() != vc.height {
		resized = gocv.NewMat()
		gocv.Resize(frame, &resized, image.Pt(vc.width, vc.height), 0, 0, gocv.InterpolationLinear)
		defer resized.Close()
	} else {
		resized = frame
	}

	bgra := gocv.NewMat()
	defer bgra.Close()
	gocv.CvtColor(resized, &bgra, gocv.ColorBGRToBGRA)
	if bgra.Empty() {
		return nil, fmt.Errorf("BGRA conversion produced empty mat")
	}

	rows := bgra.Rows()
	cols := bgra.Cols()
	data := bgra.ToBytes()
	if len(data) == 0 {
		return nil, fmt.Errorf("failed to get BGRA data")
	}

	bytesPerLine := vc.bytesPerLine
	expectedStride := cols * 4
	if bytesPerLine <= 0 || bytesPerLine < expectedStride {
		bytesPerLine = expectedStride
	}
	if bytesPerLine == expectedStride {
		return data, nil
	}

	needed := rows * bytesPerLine
	if cap(vc.bgrPaddedBuf) < needed {
		vc.bgrPaddedBuf = make([]byte, needed)
	}
	padded := vc.bgrPaddedBuf[:needed]
	for y := 0; y < rows; y++ {
		srcStart := y * expectedStride
		dstStart := y * bytesPerLine
		copy(padded[dstStart:dstStart+expectedStride], data[srcStart:srcStart+expectedStride])
	}

	return padded, nil
}

// convertToYUYVBytes converts a BGR frame to YUYV format and returns raw bytes
func (vc *LinuxVirtualCamera) convertToYUYVBytes(frame gocv.Mat) ([]byte, error) {
	// Resize if needed
	var resized gocv.Mat
	if frame.Cols() != vc.width || frame.Rows() != vc.height {
		resized = gocv.NewMat()
		gocv.Resize(frame, &resized, image.Pt(vc.width, vc.height), 0, 0, gocv.InterpolationLinear)
		defer resized.Close()
	} else {
		resized = frame
	}

	// Try using OpenCV's built-in color conversion to YUV first
	// Then manually pack to YUYV format
	yuv := gocv.NewMat()
	defer yuv.Close()
	gocv.CvtColor(resized, &yuv, gocv.ColorBGRToYUV)

	if yuv.Empty() {
		return nil, fmt.Errorf("YUV conversion produced empty mat")
	}

	rows := yuv.Rows()
	cols := yuv.Cols()
	yuvData := yuv.ToBytes()

	// Pack YUV444 to YUYV422: Y0 U Y1 V (for each pair of pixels)
	bytesPerLine := vc.bytesPerLine
	expectedStride := cols * 2
	if bytesPerLine <= 0 || bytesPerLine < expectedStride {
		bytesPerLine = expectedStride
	}
	needed := rows * bytesPerLine
	if cap(vc.yuyvBuf) < needed {
		vc.yuyvBuf = make([]byte, needed)
	}
	yuyvData := vc.yuyvBuf[:needed]

	for y := 0; y < rows; y++ {
		lineOffset := y * bytesPerLine
		for x := 0; x < cols; x += 2 {
			// First pixel YUV
			idx0 := (y*cols + x) * 3
			y0 := yuvData[idx0]
			u0 := yuvData[idx0+1]
			v0 := yuvData[idx0+2]

			// Second pixel YUV (or duplicate if at edge)
			var y1, u1, v1 byte
			if x+1 < cols {
				idx1 := (y*cols + x + 1) * 3
				y1 = yuvData[idx1]
				u1 = yuvData[idx1+1]
				v1 = yuvData[idx1+2]
			} else {
				y1, u1, v1 = y0, u0, v0
			}

			// Average U and V for 4:2:2 subsampling
			uAvg := byte((int(u0) + int(u1)) / 2)
			vAvg := byte((int(v0) + int(v1)) / 2)

			// Pack as YUYV: Y0 U Y1 V
			outIdx := lineOffset + x*2
			yuyvData[outIdx] = y0
			yuyvData[outIdx+1] = uAvg
			if x+1 < cols {
				yuyvData[outIdx+2] = y1
				yuyvData[outIdx+3] = vAvg
			}
		}
	}

	return yuyvData, nil
}

// convertToYUYV converts a BGR frame to YUYV format (deprecated - use convertToYUYVBytes)
func (vc *LinuxVirtualCamera) convertToYUYV(frame gocv.Mat) (gocv.Mat, error) {
	data, err := vc.convertToYUYVBytes(frame)
	if err != nil {
		return gocv.Mat{}, err
	}

	// Create a 2-channel Mat for compatibility
	yuyv, err := gocv.NewMatFromBytes(vc.height, vc.width, gocv.MatTypeCV8UC2, data)
	if err != nil {
		return gocv.Mat{}, fmt.Errorf("failed to create YUYV Mat: %w", err)
	}

	return yuyv, nil
}

// encodeToMJPEG encodes a BGR frame to MJPEG format
func (vc *LinuxVirtualCamera) encodeToMJPEG(frame gocv.Mat) (gocv.Mat, error) {
	// Resize if needed
	var resized gocv.Mat
	if frame.Cols() != vc.width || frame.Rows() != vc.height {
		resized = gocv.NewMat()
		gocv.Resize(frame, &resized, image.Pt(vc.width, vc.height), 0, 0, gocv.InterpolationLinear)
		defer resized.Close()
	} else {
		resized = frame
	}

	// Encode to JPEG using OpenCV
	params := []int{
		gocv.IMWriteJpegQuality, 90, // Quality 0-100
	}

	buf, err := gocv.IMEncodeWithParams(".jpg", resized, params)
	if err != nil {
		return gocv.Mat{}, fmt.Errorf("failed to encode frame to JPEG: %w", err)
	}
	defer buf.Close()

	// Get bytes from NativeByteBuffer
	jpegBytes := buf.GetBytes()
	if len(jpegBytes) == 0 {
		return gocv.Mat{}, fmt.Errorf("encoded JPEG is empty")
	}

	// For MJPEG output, we need to write the raw JPEG bytes directly
	// Instead of creating a Mat, we'll handle MJPEG specially in WriteFrame
	// For now, create a Mat that holds the JPEG bytes (as a single row)
	// Note: This is a workaround - we should ideally write JPEG bytes directly
	if len(jpegBytes) == 0 {
		return gocv.Mat{}, fmt.Errorf("JPEG encoding produced empty result")
	}

	// Copy the bytes to ensure they're not garbage collected
	jpegBytesCopy := make([]byte, len(jpegBytes))
	copy(jpegBytesCopy, jpegBytes)

	encoded, err := gocv.NewMatFromBytes(1, len(jpegBytesCopy), gocv.MatTypeCV8UC1, jpegBytesCopy)
	if err != nil {
		return gocv.Mat{}, fmt.Errorf("failed to create Mat from JPEG bytes: %w", err)
	}

	if encoded.Empty() {
		return gocv.Mat{}, fmt.Errorf("created Mat is empty")
	}

	return encoded, nil
}

// Close closes the virtual camera device
func (vc *LinuxVirtualCamera) Close() error {
	if !vc.open {
		return nil
	}

	// Stop streaming
	bufType := V4L2_BUF_TYPE_VIDEO_OUTPUT
	ioctl(vc.fd, VIDIOC_STREAMOFF, uintptr(unsafe.Pointer(&bufType)))

	// Close device
	err := syscall.Close(vc.fd)
	vc.open = false
	return err
}

// IsOpen returns whether the virtual camera is open
func (vc *LinuxVirtualCamera) IsOpen() bool {
	return vc.open
}

// GetDevicePath returns the device path
func (vc *LinuxVirtualCamera) GetDevicePath() string {
	return vc.devicePath
}

// ioctl performs a v4l2 ioctl call
func ioctl(fd int, request uintptr, argp uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), request, argp)
	if errno != 0 {
		return errno
	}
	return nil
}

// FindAvailableVirtualCamera finds an available v4l2loopback device
// Returns the device path (e.g., "/dev/video2") or error if none found
func FindAvailableVirtualCamera() (string, error) {
	// Check common v4l2loopback device paths
	// Usually v4l2loopback devices start at /dev/video2 or higher
	for i := 2; i < 10; i++ {
		devicePath := fmt.Sprintf("/dev/video%d", i)
		if _, err := os.Stat(devicePath); err == nil {
			// Try to open and query capabilities
			fd, err := syscall.Open(devicePath, syscall.O_RDWR|syscall.O_NONBLOCK, 0)
			if err != nil {
				continue
			}

			var cap v4l2_capability
			err = ioctl(fd, VIDIOC_QUERYCAP, uintptr(unsafe.Pointer(&cap)))
			syscall.Close(fd)

			if err == nil {
				// Check if it's a v4l2loopback device (capabilities include VIDEO_OUTPUT)
				if cap.capabilities&V4L2_CAP_VIDEO_OUTPUT != 0 {
					return devicePath, nil
				}
			}
		}
	}

	return "", fmt.Errorf("no available v4l2loopback device found (install with: sudo apt install v4l2loopback-dkms && sudo modprobe v4l2loopback)")
}
