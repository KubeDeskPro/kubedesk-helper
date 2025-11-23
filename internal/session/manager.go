package session

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SessionType represents the type of session
type SessionType string

const (
	TypePortForward SessionType = "port-forward"
	TypeExec        SessionType = "exec"
	TypeProxy       SessionType = "proxy"
	TypeShell       SessionType = "shell"
)

// SessionStatus represents the status of a session
type SessionStatus string

const (
	StatusRunning SessionStatus = "running"
	StatusStopped SessionStatus = "stopped"
	StatusFailed  SessionStatus = "failed"
)

// Session represents a long-running kubectl process
type Session struct {
	ID           string
	Type         SessionType
	Status       SessionStatus
	StartedAt    time.Time
	Cmd          *exec.Cmd
	Namespace    string
	ResourceType string
	ResourceName string
	ServicePort  string
	LocalPort    string
	PodName      string
	Container    string
	Command      []string
	Port         int
	Context      string
	Kubeconfig   string

	// For exec and shell sessions
	stdin        io.WriteCloser
	outputBuffer *bytes.Buffer
	outputMutex  sync.RWMutex
	lastReadTime time.Time
	WriteInput   func(string) error

	// For shell sessions
	ShellCommand string
	ExitCode     *int32

	// Temporary files to clean up when session ends
	TempFiles []string
}

// Manager manages all active sessions
type Manager struct {
	sessions              map[string]*Session
	mu                    sync.RWMutex
	inactivityTimeout     time.Duration
	completedTimeout      time.Duration
	cleanupInterval       time.Duration
	stopCleanup           chan struct{}
	onSessionCleanup      func(string) // Callback for cleanup (e.g., delete temp files)
}

// NewManager creates a new session manager
func NewManager() *Manager {
	m := &Manager{
		sessions:          make(map[string]*Session),
		inactivityTimeout: 30 * time.Minute, // Remove inactive sessions after 30 minutes
		completedTimeout:  5 * time.Minute,  // Remove completed sessions after 5 minutes
		cleanupInterval:   1 * time.Minute,  // Check every minute
		stopCleanup:       make(chan struct{}),
	}

	// Start background cleanup goroutine
	go m.cleanupLoop()

	return m
}

// SetInactivityTimeout sets the timeout for inactive sessions
func (m *Manager) SetInactivityTimeout(timeout time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inactivityTimeout = timeout
}

// SetCompletedTimeout sets the timeout for completed sessions
func (m *Manager) SetCompletedTimeout(timeout time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completedTimeout = timeout
}

// SetCleanupCallback sets a callback function that's called when a session is cleaned up
func (m *Manager) SetCleanupCallback(callback func(string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSessionCleanup = callback
}

// Shutdown stops the cleanup goroutine
func (m *Manager) Shutdown() {
	close(m.stopCleanup)
}

// Create creates a new session
func (m *Manager) Create(sessionType SessionType) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	session := &Session{
		ID:           uuid.New().String(),
		Type:         sessionType,
		Status:       StatusRunning,
		StartedAt:    time.Now(),
		outputBuffer: &bytes.Buffer{},
		lastReadTime: time.Now(),
	}

	m.sessions[session.ID] = session
	slog.Info("Session created", "id", session.ID, "type", sessionType)
	return session
}

// Get retrieves a session by ID
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[id]
	return session, ok
}

// List returns all sessions of a specific type
func (m *Manager) List(sessionType SessionType) []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Session
	for _, session := range m.sessions {
		if session.Type == sessionType {
			result = append(result, session)
		}
	}
	return result
}

// Stop stops a session and removes it
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok {
		return nil // Already stopped
	}

	if session.Cmd != nil && session.Cmd.Process != nil {
		if err := session.Cmd.Process.Kill(); err != nil {
			slog.Warn("Failed to kill process", "id", id, "error", err)
		}
	}

	session.Status = StatusStopped

	// Clean up temporary files
	m.cleanupSessionFiles(session)

	// Call cleanup callback if set
	if m.onSessionCleanup != nil {
		m.onSessionCleanup(id)
	}

	delete(m.sessions, id)
	slog.Info("Session stopped", "id", id)
	return nil
}

// cleanupSessionFiles removes temporary files associated with a session
func (m *Manager) cleanupSessionFiles(session *Session) {
	for _, tmpFile := range session.TempFiles {
		if err := os.Remove(tmpFile); err != nil && !os.IsNotExist(err) {
			slog.Warn("Failed to remove temp file", "file", tmpFile, "error", err)
		} else {
			slog.Debug("Removed temp file", "file", tmpFile)
		}
	}
}

// StopAll stops all sessions
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, session := range m.sessions {
		if session.Cmd != nil && session.Cmd.Process != nil {
			if err := session.Cmd.Process.Kill(); err != nil {
				slog.Warn("Failed to kill process", "id", id, "error", err)
			}
		}
		session.Status = StatusStopped

		// Clean up temporary files
		m.cleanupSessionFiles(session)

		// Call cleanup callback if set
		if m.onSessionCleanup != nil {
			m.onSessionCleanup(id)
		}
	}

	m.sessions = make(map[string]*Session)
	slog.Info("All sessions stopped")
}

// cleanupLoop runs in the background and removes inactive/completed sessions
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(m.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.cleanupInactiveSessions()
		case <-m.stopCleanup:
			return
		}
	}
}

// cleanupInactiveSessions removes sessions that have been inactive or completed for too long
func (m *Manager) cleanupInactiveSessions() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	var toRemove []string

	for id, session := range m.sessions {
		var shouldRemove bool
		var reason string

		// Check if session is completed and past the completed timeout
		if session.Status == StatusStopped || session.Status == StatusFailed {
			if now.Sub(session.lastReadTime) > m.completedTimeout {
				shouldRemove = true
				reason = "completed session timeout"
			}
		} else {
			// Check if session is inactive (no reads) for too long
			if now.Sub(session.lastReadTime) > m.inactivityTimeout {
				shouldRemove = true
				reason = "inactivity timeout"
			}
		}

		if shouldRemove {
			toRemove = append(toRemove, id)
			slog.Info("Cleaning up session",
				"id", id,
				"type", session.Type,
				"reason", reason,
				"lastReadTime", session.lastReadTime.Format(time.RFC3339),
				"age", now.Sub(session.StartedAt).String())
		}
	}

	// Remove sessions outside the iteration
	for _, id := range toRemove {
		session := m.sessions[id]

		// Kill the process if still running
		if session.Cmd != nil && session.Cmd.Process != nil {
			if err := session.Cmd.Process.Kill(); err != nil {
				slog.Warn("Failed to kill process during cleanup", "id", id, "error", err)
			}
		}

		// Clean up temporary files
		m.cleanupSessionFiles(session)

		// Call cleanup callback if set
		if m.onSessionCleanup != nil {
			m.onSessionCleanup(id)
		}

		delete(m.sessions, id)
	}

	if len(toRemove) > 0 {
		slog.Info("Cleanup completed", "removed", len(toRemove), "remaining", len(m.sessions))
	}
}



// ReadOutput reads output from an exec session and updates last read time
func (s *Session) ReadOutput() string {
	s.outputMutex.Lock()
	defer s.outputMutex.Unlock()

	output := s.outputBuffer.String()
	s.lastReadTime = time.Now() // Update activity timestamp
	return output
}

// GetOutputBuffer returns the output buffer for writing
func (s *Session) GetOutputBuffer() io.Writer {
	return &threadSafeWriter{buffer: s.outputBuffer, mutex: &s.outputMutex}
}

// threadSafeWriter wraps a buffer with a mutex for thread-safe writes
type threadSafeWriter struct {
	buffer *bytes.Buffer
	mutex  *sync.RWMutex
}

func (w *threadSafeWriter) Write(p []byte) (n int, err error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.buffer.Write(p)
}

