package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/kubedeskpro/kubedesk-helper/internal/cluster"
	"github.com/kubedeskpro/kubedesk-helper/internal/env"
	"github.com/kubedeskpro/kubedesk-helper/internal/session"
)

// ShellHandler handles shell session endpoints
type ShellHandler struct {
	sessionMgr *session.Manager
}

// ShellStartRequest represents a shell command start request
type ShellStartRequest struct {
	Command     string `json:"command"`              // Full shell command string
	Kubeconfig  string `json:"kubeconfig,omitempty"` // Optional kubeconfig content
	Context     string `json:"context,omitempty"`    // Optional kubectl context
	ClusterHash string `json:"clusterHash,omitempty"` // Optional: computed by helper if not provided
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

	// If kubeconfig/context not provided, try to look up from registry
	if req.Kubeconfig == "" && req.Context == "" && req.ClusterHash != "" {
		regKubeconfig, regContext, foundInRegistry := cluster.GetRegistry().Lookup(req.ClusterHash)
		if !foundInRegistry {
			slog.Error("Cluster hash not found in registry and kubeconfig/context not provided",
				"providedHash", req.ClusterHash,
				"command", req.Command,
				"hint", "This usually happens after helper restart. App should send kubeconfig and context.",
			)
			http.Error(w, "Cluster hash not found in registry. Please provide kubeconfig and context in the request.", http.StatusBadRequest)
			return
		}
		req.Kubeconfig = regKubeconfig
		req.Context = regContext
		slog.Info("Looked up cluster info from registry",
			"clusterHash", req.ClusterHash,
			"context", req.Context,
		)
	}

	// Compute cluster hash if not provided
	if req.ClusterHash == "" {
		req.ClusterHash = cluster.ComputeAndRegister(req.Kubeconfig, req.Context)
	} else {
		// If hash is provided, VALIDATE it first before registering
		expectedHash := cluster.ComputeHash(req.Kubeconfig, req.Context)
		if req.ClusterHash != expectedHash {
			slog.Error("Cluster hash mismatch - app sent wrong hash!",
				"providedHash", req.ClusterHash,
				"expectedHash", expectedHash,
				"context", req.Context,
			)
			http.Error(w, fmt.Sprintf("Cluster hash mismatch: expected %s, got %s", expectedHash, req.ClusterHash), http.StatusBadRequest)
			return
		}

		// Hash is valid - register it
		cluster.GetRegistry().Register(req.ClusterHash, req.Kubeconfig, req.Context)
		slog.Info("Validated and registered cluster hash",
			"clusterHash", req.ClusterHash,
			"context", req.Context,
		)
	}

	// Double-check validation (should always pass now)
	if !cluster.ValidateHash(req.ClusterHash, req.Kubeconfig, req.Context) {
		expectedHash := cluster.GetExpectedHash(req.Kubeconfig, req.Context)
		slog.Error("Cluster hash validation failed",
			"providedHash", req.ClusterHash,
			"expectedHash", expectedHash,
			"kubeconfig", req.Kubeconfig,
			"context", req.Context,
			"command", req.Command,
		)
		http.Error(w, "Cluster hash validation failed", http.StatusBadRequest)
		return
	}

	// Create session
	sess := h.sessionMgr.Create(session.TypeShell)
	sess.ShellCommand = req.Command
	sess.Context = req.Context
	sess.Kubeconfig = req.Kubeconfig
	sess.ClusterHash = req.ClusterHash

	// Inject --context flag into kubectl commands if context is provided
	command := req.Command
	if req.Context != "" {
		// Replace kubectl commands with kubectl --context=<context>
		// This handles various kubectl command patterns
		command = injectKubectlContext(command, req.Context)
		slog.Info("Injected context into command", "sessionId", sess.ID, "original", req.Command, "modified", command, "context", req.Context)
	}

	slog.Info("Starting shell session", "sessionId", sess.ID, "command", command, "clusterHash", req.ClusterHash)

	// Build bash command
	cmd := exec.Command("/bin/bash", "-c", command)
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

		// Register temp file for cleanup when session ends
		sess.TempFiles = append(sess.TempFiles, tmpFile)
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
		// CRITICAL: Clean up temp files AFTER command finishes
		// This ensures kubectl can read the kubeconfig file for the entire duration
		defer func() {
			for _, tmpFile := range sess.TempFiles {
				if err := os.Remove(tmpFile); err != nil && !os.IsNotExist(err) {
					slog.Warn("Failed to remove temp file", "file", tmpFile, "error", err)
				} else {
					slog.Debug("Removed temp file after shell completed", "file", tmpFile)
				}
			}
			// Clear the list so session cleanup doesn't try to delete them again
			sess.TempFiles = nil
		}()

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

	// Get cluster hash from query parameter (optional)
	clusterHash := r.URL.Query().Get("clusterHash")

	// Get session with cluster validation if hash provided
	var sess *session.Session
	var ok bool
	if clusterHash != "" {
		sess, ok = h.sessionMgr.GetWithClusterValidation(sessionID, clusterHash)
		if !ok {
			slog.Warn("Session not found or cluster hash mismatch",
				"sessionId", sessionID,
				"providedHash", clusterHash,
			)
			http.Error(w, "Session not found or cluster mismatch", http.StatusNotFound)
			return
		}
	} else {
		sess, ok = h.sessionMgr.Get(sessionID)
		if !ok {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
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

	// Get cluster hash from query parameter (optional)
	clusterHash := r.URL.Query().Get("clusterHash")

	// Validate cluster hash if provided
	if clusterHash != "" {
		sess, ok := h.sessionMgr.GetWithClusterValidation(sessionID, clusterHash)
		if !ok {
			slog.Warn("Session not found or cluster hash mismatch",
				"sessionId", sessionID,
				"providedHash", clusterHash,
			)
			http.Error(w, "Session not found or cluster mismatch", http.StatusNotFound)
			return
		}
		_ = sess // We just needed to validate
	}

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

// injectKubectlContext scans the command string for kubectl invocations and injects --context flag
// This handles:
// - Simple commands: "kubectl get pods"
// - Chained commands: "kubectl get pods && kubectl get svc"
// - Mixed commands: "echo hello && kubectl get pods && ls -la"
// - Pipes: "kubectl get pods | grep nginx"
// - Already has context: "kubectl --context=foo get pods" (skips)
func injectKubectlContext(command, context string) string {
	if context == "" {
		return command
	}

	// Check if command already has --context flag, if so skip injection
	if strings.Contains(command, "--context") {
		return command
	}

	contextFlag := fmt.Sprintf("--context=%s", context)

	// Use regex to find kubectl followed by whitespace
	// Pattern: \bkubectl\b - word boundary + kubectl + word boundary (prevents matching "mykubectl")
	// Followed by whitespace (\s+)
	pattern := regexp.MustCompile(`\bkubectl\b(\s+)`)

	// Replace kubectl with kubectl --context=<context>
	// $1 preserves the original whitespace
	result := pattern.ReplaceAllString(command, "kubectl "+contextFlag+"$1")

	return result
}

