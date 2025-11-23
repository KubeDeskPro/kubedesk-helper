package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/gorilla/mux"
	"github.com/kubedeskpro/kubedesk-helper/internal/env"
	"github.com/kubedeskpro/kubedesk-helper/internal/session"
)

// ShellHandler handles shell session endpoints
type ShellHandler struct {
	sessionMgr *session.Manager
}

// ShellStartRequest represents a shell command start request
type ShellStartRequest struct {
	Command    string `json:"command"`              // Full shell command string
	Kubeconfig string `json:"kubeconfig,omitempty"` // Optional kubeconfig content
	Context    string `json:"context,omitempty"`    // Optional kubectl context
}

// ShellStartResponse represents a shell start response
type ShellStartResponse struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
}

// ShellOutputResponse represents a shell output response
type ShellOutputResponse struct {
	Output    string `json:"output"`
	Timestamp string `json:"timestamp"`
	Status    string `json:"status"`
	ExitCode  *int32 `json:"exitCode,omitempty"` // Only set when process has exited
}

// Start handles POST /shell/start
func (h *ShellHandler) Start(w http.ResponseWriter, r *http.Request) {
	var req ShellStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode shell request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Command == "" {
		http.Error(w, "No command provided", http.StatusBadRequest)
		return
	}

	// Create session
	sess := h.sessionMgr.Create(session.TypeShell)
	sess.ShellCommand = req.Command
	sess.Context = req.Context
	sess.Kubeconfig = req.Kubeconfig

	slog.Info("Starting shell session", "sessionId", sess.ID, "command", req.Command)

	// Build bash command
	cmd := exec.Command("/bin/bash", "-c", req.Command)
	cmd.Env = env.GetShellEnvironment()

	// Set kubeconfig if provided
	if req.Kubeconfig != "" {
		tmpDir := os.TempDir()
		tmpFile := filepath.Join(tmpDir, fmt.Sprintf("kubeconfig-%s", sess.ID))
		if err := os.WriteFile(tmpFile, []byte(req.Kubeconfig), 0600); err != nil {
			h.sessionMgr.Stop(sess.ID)
			slog.Error("Failed to write kubeconfig", "error", err)
			http.Error(w, "Failed to write kubeconfig", http.StatusInternalServerError)
			return
		}
		cmd.Env = append(cmd.Env, fmt.Sprintf("KUBECONFIG=%s", tmpFile))

		// Clean up kubeconfig when session ends
		go func() {
			// Wait for session to be removed
			for {
				time.Sleep(1 * time.Second)
				if _, ok := h.sessionMgr.Get(sess.ID); !ok {
					os.Remove(tmpFile)
					break
				}
			}
		}()
	}

	// Set kubectl context if provided
	if req.Context != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("KUBECTL_CONTEXT=%s", req.Context))
	}

	// Capture combined output (stdout + stderr)
	cmd.Stdout = sess.GetOutputBuffer()
	cmd.Stderr = sess.GetOutputBuffer()

	sess.Cmd = cmd

	// Start the command
	if err := cmd.Start(); err != nil {
		h.sessionMgr.Stop(sess.ID)
		slog.Error("Failed to start shell command", "error", err, "command", req.Command)
		http.Error(w, fmt.Sprintf("Failed to start command: %v", err), http.StatusInternalServerError)
		return
	}

	// Monitor process completion in background
	go func() {
		err := cmd.Wait()
		var exitCode int32
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = int32(exitErr.ExitCode())
			} else {
				exitCode = 1
			}
		} else {
			exitCode = 0
		}

		// Store exit code in session
		if s, ok := h.sessionMgr.Get(sess.ID); ok {
			s.ExitCode = &exitCode
			s.Status = session.StatusStopped
		}

		slog.Info("Shell command completed", "sessionId", sess.ID, "exitCode", exitCode)
	}()

	response := ShellStartResponse{
		SessionID: sess.ID,
		Status:    "running",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Output handles GET /shell/output/{sessionId}
func (h *ShellHandler) Output(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["sessionId"]

	sess, ok := h.sessionMgr.Get(sessionID)
	if !ok {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	output := sess.ReadOutput()
	status := string(sess.Status)

	response := ShellOutputResponse{
		Output:    output,
		Timestamp: time.Now().Format(time.RFC3339),
		Status:    status,
		ExitCode:  sess.ExitCode,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Stop handles DELETE /shell/stop/{sessionId}
func (h *ShellHandler) Stop(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["sessionId"]

	if err := h.sessionMgr.Stop(sessionID); err != nil {
		slog.Error("Failed to stop shell session", "error", err, "sessionId", sessionID)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Session stopped"})
}

// List handles GET /shell/list
func (h *ShellHandler) List(w http.ResponseWriter, r *http.Request) {
	sessions := h.sessionMgr.List(session.TypeShell)

	type shellSessionInfo struct {
		SessionID string `json:"sessionId"`
		Command   string `json:"command"`
		Status    string `json:"status"`
		StartedAt string `json:"startedAt"`
		ExitCode  *int32 `json:"exitCode,omitempty"`
	}

	var result []shellSessionInfo
	for _, sess := range sessions {
		result = append(result, shellSessionInfo{
			SessionID: sess.ID,
			Command:   sess.ShellCommand,
			Status:    string(sess.Status),
			StartedAt: sess.StartedAt.Format(time.RFC3339),
			ExitCode:  sess.ExitCode,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"sessions": result})
}

