package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/kubedeskpro/kubedesk-helper/internal/session"
)

// SessionCleanupHandler handles session cleanup operations
type SessionCleanupHandler struct {
	sessionMgr *session.Manager
}

// NewSessionCleanupHandler creates a new session cleanup handler
func NewSessionCleanupHandler(sessionMgr *session.Manager) *SessionCleanupHandler {
	return &SessionCleanupHandler{
		sessionMgr: sessionMgr,
	}
}

// SessionCleanupRequest represents a session cleanup request
type SessionCleanupRequest struct {
	ClusterHash string `json:"clusterHash"`
}

// SessionCleanupResponse represents a session cleanup response
type SessionCleanupResponse struct {
	SessionsRemoved int    `json:"sessionsRemoved"`
	ClusterHash     string `json:"clusterHash"`
}

// Cleanup handles POST /sessions/cleanup
func (h *SessionCleanupHandler) Cleanup(w http.ResponseWriter, r *http.Request) {
	var req SessionCleanupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode cleanup request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.ClusterHash == "" {
		http.Error(w, "clusterHash is required", http.StatusBadRequest)
		return
	}

	slog.Info("Cleaning up sessions for cluster", "clusterHash", req.ClusterHash)

	count := h.sessionMgr.CleanupByClusterHash(req.ClusterHash)

	slog.Info("Cleaned up sessions", "count", count, "clusterHash", req.ClusterHash)

	response := SessionCleanupResponse{
		SessionsRemoved: count,
		ClusterHash:     req.ClusterHash,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

