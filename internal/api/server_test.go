package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestLocalOnly(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := localOnly(ok)

	cases := []struct {
		name    string
		host    string
		headers map[string]string
		want    int
	}{
		{"curl to localhost", "localhost:8090", nil, http.StatusOK},
		{"curl to 127.0.0.1", "127.0.0.1:8090", nil, http.StatusOK},
		{"IPv6 loopback", "[::1]:8090", nil, http.StatusOK},
		{"same-origin browser", "localhost:8090", map[string]string{"Origin": "http://localhost:8090", "Sec-Fetch-Site": "same-origin"}, http.StatusOK},
		{"DNS rebinding", "evil.example:8090", nil, http.StatusForbidden},
		{"cross-origin page", "localhost:8090", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"null origin", "localhost:8090", map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"cross-site no-cors POST", "localhost:8090", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/toggle", nil)
			req.Host = tc.host
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestCamerasWithoutPipeline(t *testing.T) {
	s := NewServer(0, func() (bool, time.Time) { return false, time.Now() }, func() {})
	rec := httptest.NewRecorder()
	s.proxyToPipeline(s.handleCameras)(rec, httptest.NewRequest(http.MethodGet, "/cameras", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotImplemented)
	}
}

func TestCamerasProxiedToPipeline(t *testing.T) {
	// Stand-in for the pipeline subprocess camera control server
	child := NewServer(0, nil, nil)
	child.SetCameraFunctions(
		func() ([]string, int) { return []string{"cam0", "cam1"}, 1 },
		func(int) error { return nil },
		func() error { return nil },
		func() error { return nil },
	)
	childMux := http.NewServeMux()
	child.registerCameraRoutes(childMux, func(h http.HandlerFunc) http.HandlerFunc { return h })
	backend := httptest.NewServer(childMux)
	defer backend.Close()

	u, _ := url.Parse(backend.URL)
	port, _ := strconv.Atoi(u.Port())

	parent := NewServer(0, func() (bool, time.Time) { return true, time.Now() }, func() {})
	parent.SetPipelinePort(port)

	rec := httptest.NewRecorder()
	parent.proxyToPipeline(parent.handleCameras)(rec, httptest.NewRequest(http.MethodGet, "/cameras", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp CamerasResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.CameraCount != 2 || resp.ActiveCamera != 1 {
		t.Errorf("got %+v, want 2 cameras with camera 1 active", resp)
	}
}
