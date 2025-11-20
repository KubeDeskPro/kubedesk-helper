package session

import (
	"bytes"
	"io"
	"log/slog"
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
	
	// For exec sessions
	stdin        io.WriteCloser
	outputBuffer *bytes.Buffer
	outputMutex  sync.RWMutex
	lastReadTime time.Time
	WriteInput   func(string) error
}

// Manager manages all active sessions
type Manager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewManager creates a new session manager
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
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
	delete(m.sessions, id)
	slog.Info("Session stopped", "id", id)
	return nil
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
	}

	m.sessions = make(map[string]*Session)
	slog.Info("All sessions stopped")
}



// ReadOutput reads output from an exec session since the last read
func (s *Session) ReadOutput() string {
	s.outputMutex.RLock()
	defer s.outputMutex.RUnlock()

	output := s.outputBuffer.String()
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

