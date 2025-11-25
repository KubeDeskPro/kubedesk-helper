package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gorilla/mux"
	"github.com/kubedeskpro/kubedesk-helper/internal/cluster"
	"github.com/kubedeskpro/kubedesk-helper/internal/env"
	"github.com/kubedeskpro/kubedesk-helper/internal/session"
)

// ExecHandler handles exec session endpoints
type ExecHandler struct {
	sessionMgr *session.Manager
}

// ExecStartRequest represents an exec start request
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
}

// Start handles POST /exec/start
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
		// Register the hash with kubeconfig/context for future lookups
		cluster.GetRegistry().Register(req.ClusterHash, req.Kubeconfig, req.Context)
	}

	// Validate cluster hash
	if !cluster.ValidateHash(req.ClusterHash, req.Kubeconfig, req.Context) {
		expectedHash := cluster.GetExpectedHash(req.Kubeconfig, req.Context)
		slog.Error("Cluster hash validation failed",
			"providedHash", req.ClusterHash,
			"expectedHash", expectedHash,
			"kubeconfig", req.Kubeconfig,
			"context", req.Context,
			"pod", req.PodName,
		)
		http.Error(w, "Cluster hash validation failed", http.StatusBadRequest)
		return
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

	// Monitor process in background
	go func() {
		cmd.Wait()
		sess.Status = session.StatusStopped
		slog.Info("Exec session ended", "id", sess.ID)
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

