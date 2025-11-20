package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/kubedeskpro/kubedesk-helper/internal/kubectl"
)

// ExecAuthHandler handles /exec-auth endpoint
type ExecAuthHandler struct{}

// ExecAuthRequest represents an exec-auth command request
type ExecAuthRequest struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

// ExecAuthResponse represents an exec-auth command response
type ExecAuthResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int32  `json:"exitCode"`
}

// Handle processes exec-auth command requests
func (h *ExecAuthHandler) Handle(w http.ResponseWriter, r *http.Request) {
	var req ExecAuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode exec-auth request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Command == "" {
		http.Error(w, "No command provided", http.StatusBadRequest)
		return
	}

	// Execute command with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := kubectl.ExecuteCommand(ctx, req.Command, req.Args, req.Env)
	if err != nil {
		slog.Error("Failed to execute exec-auth command", "error", err, "command", req.Command)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := ExecAuthResponse{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

