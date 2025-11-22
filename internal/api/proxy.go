package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/kubedeskpro/kubedesk-helper/internal/env"
	"github.com/kubedeskpro/kubedesk-helper/internal/session"
)

// ProxyHandler handles proxy endpoints
type ProxyHandler struct {
	sessionMgr *session.Manager
}

// ProxyStartRequest represents a proxy start request
type ProxyStartRequest struct {
	Port       int    `json:"port"`
	Kubeconfig string `json:"kubeconfig,omitempty"`
	Context    string `json:"context,omitempty"`
}

// ProxyStartResponse represents a proxy start response
type ProxyStartResponse struct {
	SessionID string `json:"sessionId"`
	Port      int    `json:"port"`
	Status    string `json:"status"`
}

// ProxyListResponse represents a proxy list response
type ProxyListResponse struct {
	Sessions []ProxySessionInfo `json:"sessions"`
}

// ProxySessionInfo represents proxy session information
type ProxySessionInfo struct {
	SessionID string `json:"sessionId"`
	Port      int    `json:"port"`
	Context   string `json:"context"`
	Status    string `json:"status"`
	StartedAt string `json:"startedAt"`
}

// Start handles POST /proxy/start
func (h *ProxyHandler) Start(w http.ResponseWriter, r *http.Request) {
	var req ProxyStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode proxy request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Default port
	if req.Port == 0 {
		req.Port = 8001
	}

	// Create session
	sess := h.sessionMgr.Create(session.TypeProxy)
	sess.Port = req.Port
	sess.Context = req.Context
	sess.Kubeconfig = req.Kubeconfig

	// Find kubectl
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		h.sessionMgr.Stop(sess.ID)
		http.Error(w, "kubectl not found in PATH", http.StatusInternalServerError)
		return
	}

	// Build kubectl proxy command
	args := []string{"proxy"}
	if req.Context != "" {
		args = append(args, "--context", req.Context)
	}
	args = append(args, "--port", strconv.Itoa(req.Port))

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
	}

	sess.Cmd = cmd

	// Start proxy in background
	if err := cmd.Start(); err != nil {
		h.sessionMgr.Stop(sess.ID)
		slog.Error("Failed to start proxy", "error", err)
		http.Error(w, fmt.Sprintf("Failed to start proxy: %v", err), http.StatusInternalServerError)
		return
	}

	// Monitor process in background
	go func() {
		cmd.Wait()
		sess.Status = session.StatusStopped
		slog.Info("Proxy session ended", "id", sess.ID)
	}()

	// CRITICAL: Wait for kubectl proxy to actually start listening on the port
	// kubectl proxy might start but fail immediately (auth errors, port in use, etc.)
	maxRetries := 30 // 3 seconds total
	proxyReady := false
	for i := 0; i < maxRetries; i++ {
		time.Sleep(100 * time.Millisecond)

		// Check if process is still running
		if sess.Cmd.ProcessState != nil && sess.Cmd.ProcessState.Exited() {
			h.sessionMgr.Stop(sess.ID)
			slog.Error("kubectl proxy exited immediately", "port", req.Port, "context", req.Context)
			http.Error(w, "kubectl proxy failed to start (process exited)", http.StatusInternalServerError)
			return
		}

		// Try to connect to the proxy port
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", req.Port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			proxyReady = true
			break
		}
	}

	if !proxyReady {
		h.sessionMgr.Stop(sess.ID)
		slog.Error("kubectl proxy did not start listening", "port", req.Port, "context", req.Context)
		http.Error(w, "kubectl proxy failed to start listening on port", http.StatusInternalServerError)
		return
	}

	slog.Info("Proxy started and verified", "id", sess.ID, "port", req.Port, "context", req.Context)

	response := ProxyStartResponse{
		SessionID: sess.ID,
		Port:      req.Port,
		Status:    string(sess.Status),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Stop handles DELETE /proxy/stop/{sessionId}
func (h *ProxyHandler) Stop(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["sessionId"]

	if err := h.sessionMgr.Stop(sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

// List handles GET /proxy/list
func (h *ProxyHandler) List(w http.ResponseWriter, r *http.Request) {
	sessions := h.sessionMgr.List(session.TypeProxy)

	var sessionInfos []ProxySessionInfo
	for _, sess := range sessions {
		sessionInfos = append(sessionInfos, ProxySessionInfo{
			SessionID: sess.ID,
			Port:      sess.Port,
			Context:   sess.Context,
			Status:    string(sess.Status),
			StartedAt: sess.StartedAt.Format(time.RFC3339),
		})
	}

	response := ProxyListResponse{Sessions: sessionInfos}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

