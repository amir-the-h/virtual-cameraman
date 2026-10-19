package camera

import "testing"

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"rtsp://admin:hunter2@192.168.1.10:554/stream": "rtsp://admin:xxxxx@192.168.1.10:554/stream",
		"rtsp://192.168.1.10:554/stream":               "rtsp://192.168.1.10:554/stream",
		"rtsp://user@cam.local/live":                   "rtsp://user@cam.local/live",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}

	src := NewRTSPSource("rtsp://admin:hunter2@cam.local/live")
	if name := src.Name(); name != "RTSP: rtsp://admin:xxxxx@cam.local/live" {
		t.Errorf("Name() = %q, leaks or mangles credentials", name)
	}
}
