package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/gorilla/mux"
	"github.com/kubedeskpro/kubedesk-helper/internal/cluster"
	"github.com/kubedeskpro/kubedesk-helper/internal/env"
	"github.com/kubedeskpro/kubedesk-helper/internal/session"
)

// ExecHandler handles exec session endpoints
type ExecHandler struct {
	sessionMgr *session.Manager
}

// ExecRequest represents a synchronous exec request
type ExecRequest struct {
	Namespace   string   `json:"namespace"`
	PodName     string   `json:"podName"`
	Container   string   `json:"container,omitempty"`
	Command     []string `json:"command"`
	Kubeconfig  string   `json:"kubeconfig,omitempty"`
	Context     string   `json:"context,omitempty"`
	ClusterHash string   `json:"clusterHash,omitempty"` // Optional: computed by helper if not provided
	Timeout     int      `json:"timeout,omitempty"`     // Optional: max seconds to wait (default: 300)
}

// ExecResponse represents a synchronous exec response
type ExecResponse struct {
	Output   string  `json:"output"`
	ExitCode int32   `json:"exitCode"`
	Duration float64 `json:"duration"` // Seconds
	Error    string  `json:"error,omitempty"`
}

// ExecStartRequest represents an exec start request (legacy session-based API)
type ExecStartRequest struct {
	Namespace   string   `json:"namespace"`
	PodName     string   `json:"podName"`
	Container   string   `json:"container,omitempty"`
	Command     []string `json:"command"`
	Kubeconfig  string   `json:"kubeconfig,omitempty"`
	Context     string   `json:"context,omitempty"`
	ClusterHash string   `json:"clusterHash,omitempty"` // Optional: computed by helper if not provided
}

// ExecStartResponse represents an exec start response
type ExecStartResponse struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
}

// ExecInputRequest represents an exec input request
type ExecInputRequest struct {
	Input       string `json:"input"`
	ClusterHash string `json:"clusterHash,omitempty"` // Optional: for validation
}

// ExecOutputResponse represents an exec output response
type ExecOutputResponse struct {
	Output    string `json:"output"`
	Timestamp string `json:"timestamp"`
	Status    string `json:"status"`
	ExitCode  *int32 `json:"exitCode,omitempty"` // Exit code of the command (nil if still running)
}

// Execute handles POST /exec - synchronous exec (recommended)
func (h *ExecHandler) Execute(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	var req ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode exec request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate request
	if req.Namespace == "" || req.PodName == "" || len(req.Command) == 0 {
		http.Error(w, "Missing required fields: namespace, podName, command", http.StatusBadRequest)
		return
	}

	// Set default timeout
	if req.Timeout == 0 {
		req.Timeout = 300 // 5 minutes default
	}

	// Validate or compute cluster hash
	if req.ClusterHash == "" {
		req.ClusterHash = cluster.ComputeAndRegister(req.Kubeconfig, req.Context)
		slog.Debug("Computed cluster hash for exec",
			"clusterHash", req.ClusterHash,
			"context", req.Context,
		)
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

	// Find kubectl
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		slog.Error("kubectl not found in PATH", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ExecResponse{
			Output:   "",
			ExitCode: -1,
			Duration: time.Since(startTime).Seconds(),
			Error:    "kubectl not found in PATH",
		})
		return
	}

	// Build kubectl exec command
	args := []string{"exec", "-i"}
	if req.Context != "" {
		args = append(args, "--context", req.Context)
	}
	args = append(args, "-n", req.Namespace)
	if req.Container != "" {
		args = append(args, "-c", req.Container)
	}
	args = append(args, req.PodName, "--")
	args = append(args, req.Command...)

	cmd := exec.Command(kubectlPath, args...)
	cmd.Env = env.GetShellEnvironment()

	// Create temp kubeconfig file if provided
	var tmpFile string
	if req.Kubeconfig != "" {
		tmpDir := os.TempDir()
		tmpFile = filepath.Join(tmpDir, fmt.Sprintf("kubeconfig-exec-%d", time.Now().UnixNano()))
		if err := os.WriteFile(tmpFile, []byte(req.Kubeconfig), 0600); err != nil {
			slog.Error("Failed to write kubeconfig", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ExecResponse{
				Output:   "",
				ExitCode: -1,
				Duration: time.Since(startTime).Seconds(),
				Error:    "Failed to write kubeconfig",
			})
			return
		}
		// Ensure cleanup happens no matter what
		defer func() {
			if err := os.Remove(tmpFile); err != nil && !os.IsNotExist(err) {
				slog.Warn("Failed to remove temp kubeconfig", "file", tmpFile, "error", err)
			} else {
				slog.Debug("Removed temp kubeconfig", "file", tmpFile)
			}
		}()

		cmd.Env = append(cmd.Env, fmt.Sprintf("KUBECONFIG=%s", tmpFile))

		slog.Debug("Executing kubectl exec with custom kubeconfig",
			"command", kubectlPath,
			"args", args,
			"kubeconfigFile", tmpFile,
			"pod", req.PodName,
			"namespace", req.Namespace,
			"context", req.Context,
			"timeout", req.Timeout,
		)
	} else {
		slog.Debug("Executing kubectl exec with default kubeconfig",
			"command", kubectlPath,
			"args", args,
			"pod", req.PodName,
			"namespace", req.Namespace,
			"context", req.Context,
			"timeout", req.Timeout,
		)
	}

	// Run command with timeout
	ctx, cancel := r.Context(), func() {}
	if req.Timeout > 0 {
		var timeoutCtx context.Context
		timeoutCtx, cancel = context.WithTimeout(r.Context(), time.Duration(req.Timeout)*time.Second)
		ctx = timeoutCtx
	}
	defer cancel()

	cmdWithTimeout := exec.CommandContext(ctx, kubectlPath, args...)
	cmdWithTimeout.Env = cmd.Env

	// Capture combined output (stdout + stderr)
	output, err := cmdWithTimeout.CombinedOutput()
	duration := time.Since(startTime).Seconds()

	// Determine exit code
	var exitCode int32
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = int32(exitErr.ExitCode())
			slog.Info("Exec completed with error",
				"pod", req.PodName,
				"command", req.Command,
				"exitCode", exitCode,
				"duration", duration,
				"outputLength", len(output),
			)
		} else if ctx.Err() == context.DeadlineExceeded {
			exitCode = -1
			slog.Error("Exec timed out",
				"pod", req.PodName,
				"command", req.Command,
				"timeout", req.Timeout,
				"duration", duration,
			)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGatewayTimeout)
			json.NewEncoder(w).Encode(ExecResponse{
				Output:   string(output),
				ExitCode: exitCode,
				Duration: duration,
				Error:    fmt.Sprintf("Command timed out after %d seconds", req.Timeout),
			})
			return
		} else {
			exitCode = -1
			slog.Error("Exec failed",
				"pod", req.PodName,
				"command", req.Command,
				"error", err,
				"duration", duration,
			)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ExecResponse{
				Output:   string(output),
				ExitCode: exitCode,
				Duration: duration,
				Error:    err.Error(),
			})
			return
		}
	} else {
		exitCode = 0
		slog.Info("Exec completed successfully",
			"pod", req.PodName,
			"command", req.Command,
			"duration", duration,
			"outputLength", len(output),
		)
	}

	// Return response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ExecResponse{
		Output:   string(output),
		ExitCode: exitCode,
		Duration: duration,
	})
}

// Start handles POST /exec/start (legacy session-based API - deprecated)
func (h *ExecHandler) Start(w http.ResponseWriter, r *http.Request) {
	var req ExecStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode exec request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate request
	if req.Namespace == "" || req.PodName == "" || len(req.Command) == 0 {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	// If kubeconfig/context not provided, try to look up from registry
	if req.Kubeconfig == "" && req.Context == "" && req.ClusterHash != "" {
		regKubeconfig, regContext, foundInRegistry := cluster.GetRegistry().Lookup(req.ClusterHash)
		if !foundInRegistry {
			slog.Error("Cluster hash not found in registry and kubeconfig/context not provided",
				"providedHash", req.ClusterHash,
				"pod", req.PodName,
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
				"pod", req.PodName,
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

	// Create session
	sess := h.sessionMgr.Create(session.TypeExec)
	sess.Namespace = req.Namespace
	sess.PodName = req.PodName
	sess.Container = req.Container
	sess.Command = req.Command
	sess.Context = req.Context
	sess.Kubeconfig = req.Kubeconfig
	sess.ClusterHash = req.ClusterHash

	// Find kubectl
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		h.sessionMgr.Stop(sess.ID)
		http.Error(w, "kubectl not found in PATH", http.StatusInternalServerError)
		return
	}

	// Build kubectl exec command
	args := []string{"exec", "-i"}
	if req.Context != "" {
		args = append(args, "--context", req.Context)
	}
	args = append(args, "-n", req.Namespace)
	if req.Container != "" {
		args = append(args, "-c", req.Container)
	}
	args = append(args, req.PodName, "--")
	args = append(args, req.Command...)

	cmd := exec.Command(kubectlPath, args...)
	cmd.Env = env.GetShellEnvironment()

	// Set kubeconfig if provided
	if req.Kubeconfig != "" {
		tmpDir := os.TempDir()
		tmpFile := filepath.Join(tmpDir, fmt.Sprintf("kubeconfig-%s", sess.ID))
		if err := os.WriteFile(tmpFile, []byte(req.Kubeconfig), 0600); err != nil {
			h.sessionMgr.Stop(sess.ID)
			http.Error(w, "Failed to write kubeconfig", http.StatusInternalServerError)
			return
		}
		cmd.Env = append(cmd.Env, fmt.Sprintf("KUBECONFIG=%s", tmpFile))

		// Register temp file for cleanup when session ends
		sess.TempFiles = append(sess.TempFiles, tmpFile)

		slog.Debug("Executing kubectl exec with custom kubeconfig",
			"sessionId", sess.ID,
			"command", kubectlPath,
			"args", args,
			"kubeconfigFile", tmpFile,
			"pod", req.PodName,
			"namespace", req.Namespace,
			"context", req.Context,
		)
	} else {
		slog.Debug("Executing kubectl exec with default kubeconfig",
			"sessionId", sess.ID,
			"command", kubectlPath,
			"args", args,
			"pod", req.PodName,
			"namespace", req.Namespace,
			"context", req.Context,
		)
	}

	// Setup stdin/stdout/stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		h.sessionMgr.Stop(sess.ID)
		http.Error(w, "Failed to create stdin pipe", http.StatusInternalServerError)
		return
	}
	sess.WriteInput = func(input string) error {
		_, err := stdin.Write([]byte(input))
		return err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		h.sessionMgr.Stop(sess.ID)
		http.Error(w, "Failed to create stdout pipe", http.StatusInternalServerError)
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		h.sessionMgr.Stop(sess.ID)
		http.Error(w, "Failed to create stderr pipe", http.StatusInternalServerError)
		return
	}

	sess.Cmd = cmd

	// Start exec in background
	if err := cmd.Start(); err != nil {
		h.sessionMgr.Stop(sess.ID)
		slog.Error("Failed to start exec", "error", err)
		http.Error(w, fmt.Sprintf("Failed to start exec: %v", err), http.StatusInternalServerError)
		return
	}

	// Capture output in background
	go func() {
		io.Copy(sess.GetOutputBuffer(), stdout)
	}()
	go func() {
		io.Copy(sess.GetOutputBuffer(), stderr)
	}()

	// Monitor process in background and capture exit code
	go func() {
		// CRITICAL: Clean up temp files AFTER kubectl finishes
		// This ensures kubectl can read the kubeconfig file for the entire duration
		defer func() {
			for _, tmpFile := range sess.TempFiles {
				if err := os.Remove(tmpFile); err != nil && !os.IsNotExist(err) {
					slog.Warn("Failed to remove temp file", "file", tmpFile, "error", err)
				} else {
					slog.Debug("Removed temp file after exec completed", "file", tmpFile)
				}
			}
			// Clear the list so session cleanup doesn't try to delete them again
			sess.TempFiles = nil
		}()

		err := cmd.Wait()
		sess.Status = session.StatusStopped

		// Give stderr/stdout goroutines time to finish copying
		// This ensures all output is captured before we mark as stopped
		time.Sleep(100 * time.Millisecond)

		// Capture exit code
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode := int32(exitErr.ExitCode())
				sess.ExitCode = &exitCode
				output := sess.ReadOutput()
				slog.Info("Exec session ended with error",
					"id", sess.ID,
					"exitCode", exitCode,
					"output", output,
					"pod", sess.PodName,
					"command", sess.Command,
				)
			} else {
				// Non-exit error (e.g., signal)
				exitCode := int32(-1)
				sess.ExitCode = &exitCode
				output := sess.ReadOutput()
				slog.Error("Exec session ended with non-exit error",
					"id", sess.ID,
					"error", err,
					"errorType", fmt.Sprintf("%T", err),
					"output", output,
					"pod", sess.PodName,
					"command", sess.Command,
				)
			}
		} else {
			// Success
			exitCode := int32(0)
			sess.ExitCode = &exitCode
			slog.Info("Exec session ended successfully", "id", sess.ID)
		}
	}()

	slog.Info("Exec started", "id", sess.ID, "pod", req.PodName, "command", req.Command)

	response := ExecStartResponse{
		SessionID: sess.ID,
		Status:    string(sess.Status),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Input handles POST /exec/input/{sessionId}
func (h *ExecHandler) Input(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["sessionId"]

	var req ExecInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Get session with cluster validation if hash provided
	var sess *session.Session
	var ok bool
	if req.ClusterHash != "" {
		sess, ok = h.sessionMgr.GetWithClusterValidation(sessionID, req.ClusterHash)
		if !ok {
			slog.Warn("Session not found or cluster hash mismatch",
				"sessionId", sessionID,
				"providedHash", req.ClusterHash,
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

	if sess.WriteInput == nil {
		http.Error(w, "Session does not support input", http.StatusBadRequest)
		return
	}

	if err := sess.WriteInput(req.Input); err != nil {
		http.Error(w, fmt.Sprintf("Failed to write input: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// Output handles GET /exec/output/{sessionId}
func (h *ExecHandler) Output(w http.ResponseWriter, r *http.Request) {
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

	response := ExecOutputResponse{
		Output:    output,
		Timestamp: sess.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
		Status:    string(sess.Status),
		ExitCode:  sess.ExitCode, // Include exit code (nil if still running)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Stop handles DELETE /exec/stop/{sessionId}
func (h *ExecHandler) Stop(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

