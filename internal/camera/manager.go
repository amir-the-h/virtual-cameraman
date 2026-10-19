package camera

import (
	"fmt"
	"log"
	"sync"

	"gocv.io/x/gocv"
)

// Manager handles multiple camera sources and runtime switching
type Manager struct {
	sources    []Source
	activeIdx  int
	mu         sync.RWMutex
	switching  bool       // Prevents concurrent switches
	switchCond *sync.Cond // Condition variable for switch coordination
}

// NewManager creates a new camera manager with multiple sources
func NewManager(sources []Source) (*Manager, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("no camera sources provided")
	}

	// Open the first source
	if err := sources[0].Open(); err != nil {
		return nil, fmt.Errorf("open initial source: %w", err)
	}

	log.Printf("Camera manager initialized with %d source(s), active: %s", len(sources), sources[0].Name())

	m := &Manager{
		sources:   sources,
		activeIdx: 0,
	}
	m.switchCond = sync.NewCond(&m.mu)
	return m, nil
}

// Read reads from the currently active source
func (m *Manager) Read() (gocv.Mat, error) {
	m.mu.RLock()
	// If a switch is in progress, return empty frame to avoid reading from partially-switched source
	if m.switching {
		m.mu.RUnlock()
		// Return empty mat - caller should skip this frame
		return gocv.NewMat(), nil
	}
	activeSource := m.sources[m.activeIdx]
	m.mu.RUnlock()

	return activeSource.Read()
}

// SwitchTo switches to the specified source index
func (m *Manager) SwitchTo(idx int) error {
	m.mu.Lock()

	// Wait if another switch is in progress
	for m.switching {
		log.Printf("[LOCK] Waiting for previous camera switch to complete...")
		m.switchCond.Wait()
	}

	if idx < 0 || idx >= len(m.sources) {
		m.mu.Unlock()
		return fmt.Errorf("invalid source index %d (have %d sources)", idx, len(m.sources))
	}

	if idx == m.activeIdx {
		m.mu.Unlock()
		log.Printf("Already using source %d: %s", idx, m.sources[idx].Name())
		return nil
	}

	// Set switching flag to prevent concurrent switches
	m.switching = true
	oldIdx := m.activeIdx
	newSource := m.sources[idx]
	oldSource := m.sources[oldIdx]

	log.Printf("Switching from source %d (%s) to source %d (%s)...",
		oldIdx, oldSource.Name(), idx, newSource.Name())

	// Release lock before doing slow operations (opening/closing sources)
	log.Printf("[LOCK] Releasing lock for slow operations (open/close)")
	m.mu.Unlock()

	// Open the new source (this can take time, especially for RTSP)
	if !newSource.IsOpened() {
		log.Printf("Opening new source %d: %s", idx, newSource.Name())
		if err := newSource.Open(); err != nil {
			// Failed to open new source - clear switching flag
			m.mu.Lock()
			m.switching = false
			m.switchCond.Broadcast()
			m.mu.Unlock()
			return fmt.Errorf("open source %d: %w", idx, err)
		}
		log.Printf("New source opened successfully")
	}

	// CRITICAL: Close the old source BEFORE updating activeIdx
	// This prevents Read() from trying to use the new source while old source is closing
	// which can cause CGO/OpenCV segfaults
	log.Printf("Closing old source %d: %s", oldIdx, oldSource.Name())
	if err := oldSource.Close(); err != nil {
		log.Printf("Warning: failed to close previous source: %v", err)
	}
	log.Printf("Old source closed")

	// Now acquire write lock to update activeIdx atomically
	// After this point, Read() will use the new source
	log.Printf("[LOCK] Acquiring lock to update activeIdx")
	m.mu.Lock()
	m.activeIdx = idx
	log.Printf("[LOCK] Updated activeIdx to %d", idx)

	// Clear switching flag and signal waiting goroutines while we have the lock
	m.switching = false
	m.switchCond.Broadcast()
	log.Printf("[LOCK] Cleared switching flag, broadcasting to waiting goroutines")
	m.mu.Unlock()

	log.Printf("Successfully switched to: %s", newSource.Name())
	return nil
}

// GetActive returns the currently active source
func (m *Manager) GetActive() Source {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sources[m.activeIdx]
}

// GetActiveIndex returns the index of the currently active source
func (m *Manager) GetActiveIndex() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeIdx
}

// ListSources returns all available sources
func (m *Manager) ListSources() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, len(m.sources))
	for i, src := range m.sources {
		names[i] = src.Name()
	}
	return names
}

// SourceCount returns the number of available sources
func (m *Manager) SourceCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sources)
}

// Width returns the active source's frame width
func (m *Manager) Width() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sources[m.activeIdx].Width()
}

// Height returns the active source's frame height
func (m *Manager) Height() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sources[m.activeIdx].Height()
}

// FPS returns the active source's frame rate
func (m *Manager) FPS() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sources[m.activeIdx].FPS()
}

// Close closes all sources
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var lastErr error
	for i, src := range m.sources {
		if err := src.Close(); err != nil {
			log.Printf("Error closing source %d: %v", i, err)
			lastErr = err
		}
	}
	return lastErr
}
