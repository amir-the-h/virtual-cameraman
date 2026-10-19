package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// StateResponse represents the API response for state queries
type StateResponse struct {
	Running   bool      `json:"running"`
	Timestamp time.Time `json:"timestamp"`
}

// CameraInfo represents information about a camera source
type CameraInfo struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// CamerasResponse represents the API response for camera queries
type CamerasResponse struct {
	Cameras      []CameraInfo `json:"cameras"`
	ActiveCamera int          `json:"active_camera"`
	CameraCount  int          `json:"camera_count"`
}

// SwitchCameraRequest represents the request body for switching cameras
type SwitchCameraRequest struct {
	CameraID int `json:"camera_id"`
}

// SwitchCameraResponse represents the response after switching cameras
type SwitchCameraResponse struct {
	Success      bool   `json:"success"`
	ActiveCamera int    `json:"active_camera"`
	CameraName   string `json:"camera_name"`
	Message      string `json:"message,omitempty"`
}

// Server is a simple HTTP API server for querying and controlling virtualcam
type Server struct {
	port           int
	server         *http.Server
	getState       func() (bool, time.Time)
	toggleCapture  func()
	getCameras     func() ([]string, int) // Returns camera names and active index
	switchCamera   func(int) error        // Switch to camera by index
	nextCamera     func() error           // Switch to next camera
	previousCamera func() error           // Switch to previous camera
	pipelinePort   int                    // Camera control port of the pipeline subprocess (0 = not running)
	mu             sync.RWMutex
	lastUpdate     time.Time
	isRunning      bool
}

// NewServer creates a new API server
func NewServer(port int, getState func() (bool, time.Time), toggleCapture func()) *Server {
	return &Server{
		port:          port,
		getState:      getState,
		toggleCapture: toggleCapture,
		lastUpdate:    time.Now(),
	}
}

// SetCameraFunctions sets the camera management functions (optional)
func (s *Server) SetCameraFunctions(
	getCameras func() ([]string, int),
	switchCamera func(int) error,
	nextCamera func() error,
	previousCamera func() error,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCameras = getCameras
	s.switchCamera = switchCamera
	s.nextCamera = nextCamera
	s.previousCamera = previousCamera
}

// SetPipelinePort sets the port of the pipeline subprocess camera control server.
// Camera requests are forwarded there when no local camera functions are set.
// Pass 0 when the subprocess exits.
func (s *Server) SetPipelinePort(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pipelinePort = port
}

// Start starts the HTTP server in a goroutine
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// GET /state - Query current state
	mux.HandleFunc("/state", s.handleState)

	// POST /toggle - Toggle capture state
	mux.HandleFunc("/toggle", s.handleToggle)

	// GET /health - Health check
	mux.HandleFunc("/health", s.handleHealth)

	// Camera management endpoints
	s.registerCameraRoutes(mux, s.proxyToPipeline)

	// GET / - API info
	mux.HandleFunc("/", s.handleRoot)

	return s.listen(mux)
}

// StartCameraControl serves only the camera endpoints. The pipeline subprocess
// uses it so the parent API server can forward camera requests to it.
func (s *Server) StartCameraControl() error {
	mux := http.NewServeMux()
	s.registerCameraRoutes(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })
	return s.listen(mux)
}

// registerCameraRoutes adds the camera endpoints, each wrapped by wrap.
func (s *Server) registerCameraRoutes(mux *http.ServeMux, wrap func(http.HandlerFunc) http.HandlerFunc) {
	// GET /cameras - List available cameras
	mux.HandleFunc("/cameras", wrap(s.handleCameras))

	// POST /cameras/switch - Switch to a specific camera
	mux.HandleFunc("/cameras/switch", wrap(s.handleSwitchCamera))

	// POST /cameras/next - Switch to next camera
	mux.HandleFunc("/cameras/next", wrap(s.handleNextCamera))

	// POST /cameras/previous - Switch to previous camera
	mux.HandleFunc("/cameras/previous", wrap(s.handlePreviousCamera))
}

// listen binds to localhost synchronously (so port conflicts are reported)
// and serves in a goroutine.
func (s *Server) listen(handler http.Handler) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return fmt.Errorf("listen on port %d: %w", s.port, err)
	}

	s.server = &http.Server{
		Handler:      localOnly(handler),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("API server listening on http://localhost:%d", s.port)
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("API server error: %v", err)
		}
	}()

	return nil
}

// proxyToPipeline forwards camera requests to the pipeline subprocess, which
// owns the camera manager. Local camera functions, when set, take precedence.
func (s *Server) proxyToPipeline(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		local := s.getCameras != nil
		port := s.pipelinePort
		s.mu.RUnlock()

		if local || port == 0 {
			next(w, r)
			return
		}

		target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
		httputil.NewSingleHostReverseProxy(target).ServeHTTP(w, r)
	}
}

// Stop gracefully stops the HTTP server
func (s *Server) Stop(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	log.Println("Stopping API server...")
	return s.server.Shutdown(ctx)
}

// localOnly rejects requests that did not come from a local client.
// The server only binds to 127.0.0.1, but a web page open in the user's browser
// could still reach it, either cross-origin (CSRF) or via DNS rebinding. Both are
// blocked here; curl, scripts and Stream Deck plugins send neither header.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalHost(r.Host) {
			http.Error(w, "Forbidden: invalid Host header", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !isLocalHost(u.Host) {
				http.Error(w, "Forbidden: cross-origin request", http.StatusForbidden)
				return
			}
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" || site == "same-site" {
			http.Error(w, "Forbidden: cross-site request", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLocalHost reports whether a host[:port] value names the loopback interface.
func isLocalHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// handleState returns the current capture state
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	running, timestamp := s.getState()

	response := StateResponse{
		Running:   running,
		Timestamp: timestamp,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleToggle toggles the capture state
func (s *Server) handleToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Call the toggle function
	s.toggleCapture()

	// Wait a brief moment for state to update
	time.Sleep(100 * time.Millisecond)

	// Return new state
	running, timestamp := s.getState()

	response := StateResponse{
		Running:   running,
		Timestamp: timestamp,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handleHealth returns a simple health check
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "virtualcam-api",
	})
}

// handleRoot returns API information
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	endpoints := map[string]string{
		"GET /state":             "Query current capture state",
		"POST /toggle":           "Toggle capture (pause/resume)",
		"GET /health":            "Health check",
		"GET /cameras":           "List available cameras",
		"POST /cameras/switch":   "Switch to a specific camera (body: {\"camera_id\": <id>})",
		"POST /cameras/next":     "Switch to next camera",
		"POST /cameras/previous": "Switch to previous camera",
	}

	json.NewEncoder(w).Encode(map[string]any{
		"service":   "Virtual Cameraman API",
		"version":   "1.0",
		"endpoints": endpoints,
	})
}

// handleCameras returns the list of available cameras
func (s *Server) handleCameras(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	getCameras := s.getCameras
	s.mu.RUnlock()

	if getCameras == nil {
		http.Error(w, "Camera management not available", http.StatusNotImplemented)
		return
	}

	names, activeIdx := getCameras()

	cameras := make([]CameraInfo, len(names))
	for i, name := range names {
		cameras[i] = CameraInfo{
			ID:     i,
			Name:   name,
			Active: i == activeIdx,
		}
	}

	response := CamerasResponse{
		Cameras:      cameras,
		ActiveCamera: activeIdx,
		CameraCount:  len(cameras),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleSwitchCamera switches to a specific camera
func (s *Server) handleSwitchCamera(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	switchCamera := s.switchCamera
	getCameras := s.getCameras
	s.mu.RUnlock()

	if switchCamera == nil || getCameras == nil {
		http.Error(w, "Camera management not available", http.StatusNotImplemented)
		return
	}

	var req SwitchCameraRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate camera ID
	names, _ := getCameras()
	if req.CameraID < 0 || req.CameraID >= len(names) {
		response := SwitchCameraResponse{
			Success: false,
			Message: fmt.Sprintf("Invalid camera ID: %d (valid range: 0-%d)", req.CameraID, len(names)-1),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Switch to the camera
	if err := switchCamera(req.CameraID); err != nil {
		response := SwitchCameraResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to switch camera: %v", err),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Get updated camera info
	names, activeIdx := getCameras()

	response := SwitchCameraResponse{
		Success:      true,
		ActiveCamera: activeIdx,
		CameraName:   names[activeIdx],
		Message:      fmt.Sprintf("Switched to camera %d: %s", activeIdx, names[activeIdx]),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleNextCamera switches to the next camera
func (s *Server) handleNextCamera(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	nextCamera := s.nextCamera
	getCameras := s.getCameras
	s.mu.RUnlock()

	if nextCamera == nil || getCameras == nil {
		http.Error(w, "Camera management not available", http.StatusNotImplemented)
		return
	}

	if err := nextCamera(); err != nil {
		response := SwitchCameraResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to switch to next camera: %v", err),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Get updated camera info
	names, activeIdx := getCameras()

	response := SwitchCameraResponse{
		Success:      true,
		ActiveCamera: activeIdx,
		CameraName:   names[activeIdx],
		Message:      fmt.Sprintf("Switched to next camera: %s", names[activeIdx]),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handlePreviousCamera switches to the previous camera
func (s *Server) handlePreviousCamera(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	previousCamera := s.previousCamera
	getCameras := s.getCameras
	s.mu.RUnlock()

	if previousCamera == nil || getCameras == nil {
		http.Error(w, "Camera management not available", http.StatusNotImplemented)
		return
	}

	if err := previousCamera(); err != nil {
		response := SwitchCameraResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to switch to previous camera: %v", err),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Get updated camera info
	names, activeIdx := getCameras()

	response := SwitchCameraResponse{
		Success:      true,
		ActiveCamera: activeIdx,
		CameraName:   names[activeIdx],
		Message:      fmt.Sprintf("Switched to previous camera: %s", names[activeIdx]),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
