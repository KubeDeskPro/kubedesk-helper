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
	"github.com/kubedeskpro/kubedesk-helper/internal/cluster"
	"github.com/kubedeskpro/kubedesk-helper/internal/env"
	"github.com/kubedeskpro/kubedesk-helper/internal/session"
)

// ProxyHandler handles proxy endpoints
type ProxyHandler struct {
	sessionMgr *session.Manager
}

// ProxyStartRequest represents a proxy start request
type ProxyStartRequest struct {
	Port        int    `json:"port"`
	Kubeconfig  string `json:"kubeconfig,omitempty"`
	Context     string `json:"context,omitempty"`
	ClusterHash string `json:"clusterHash,omitempty"` // Optional: computed by helper if not provided
}

// ProxyStartResponse represents a proxy start response
type ProxyStartResponse struct {
	SessionID   string `json:"sessionId"`
	Port        int    `json:"port"`        // Deprecated: App should use /proxy/{clusterHash}/* instead
	ClusterHash string `json:"clusterHash"` // Use this to route requests via /proxy/{clusterHash}/*
	Status      string `json:"status"`
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

	// Compute cluster hash if not provided and register it
	if req.ClusterHash == "" {
		req.ClusterHash = cluster.ComputeAndRegister(req.Kubeconfig, req.Context)
		slog.Info("Computed and registered cluster hash",
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

	// CRITICAL: Check if there's already a proxy running for this cluster hash
	// If yes, return the existing session (performance optimization)
	// This is transparent to the app - it just gets a working proxy
	existingProxies := h.sessionMgr.FindByClusterHash(req.ClusterHash)
	for _, existing := range existingProxies {
		if existing.Type == session.TypeProxy && existing.Status == session.StatusRunning {
			// CRITICAL: Verify the context matches before reusing!
			// This prevents returning a proxy for the wrong cluster
			if existing.Context != req.Context {
				slog.Warn("Found proxy with same hash but different context - NOT reusing",
					"sessionId", existing.ID,
					"existingContext", existing.Context,
					"requestedContext", req.Context,
					"clusterHash", req.ClusterHash,
				)
				continue // Don't reuse - keep looking or create new one
			}

			// Found an existing proxy for this cluster with matching context - reuse it!
			slog.Info("Reusing existing proxy for cluster",
				"sessionId", existing.ID,
				"clusterHash", req.ClusterHash,
				"context", req.Context,
				"port", existing.Port,
			)
			response := ProxyStartResponse{
				SessionID:   existing.ID,
				Port:        existing.Port,
				ClusterHash: req.ClusterHash,
				Status:      string(existing.Status),
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
	}

	// No existing proxy for this cluster - need to start a new one
	// CRITICAL SAFETY: ALWAYS use deterministic port based on cluster hash
	// NEVER trust the app's port choice - this prevents cross-cluster contamination
	assignedPort := h.assignPortForCluster(req.ClusterHash)

	if req.Port != 0 && req.Port != assignedPort {
		slog.Warn("App requested specific port but we're using deterministic port for safety",
			"requestedPort", req.Port,
			"assignedPort", assignedPort,
			"clusterHash", req.ClusterHash,
			"reason", "Prevents cross-cluster contamination",
		)
	}

	slog.Info("Assigned deterministic port for cluster",
		"clusterHash", req.ClusterHash,
		"port", assignedPort,
		"context", req.Context,
	)

	// CRITICAL: Check if the assigned port is already in use by a DIFFERENT cluster
	// If yes, we must kill that proxy first to prevent cross-cluster contamination
	allProxies := h.sessionMgr.List(session.TypeProxy)
	for _, existing := range allProxies {
		if existing.Port == assignedPort && existing.ClusterHash != req.ClusterHash {
			// Different cluster using our port - MUST kill it
			slog.Warn("Killing proxy from different cluster on our assigned port",
				"killingSessionId", existing.ID,
				"killingClusterHash", existing.ClusterHash,
				"killingContext", existing.Context,
				"newClusterHash", req.ClusterHash,
				"newContext", req.Context,
				"port", assignedPort,
			)
			h.sessionMgr.Stop(existing.ID)
		}
	}

	// Create session
	sess := h.sessionMgr.Create(session.TypeProxy)
	sess.Port = assignedPort
	sess.Context = req.Context
	sess.Kubeconfig = req.Kubeconfig
	sess.ClusterHash = req.ClusterHash

	slog.Info("Starting new proxy session",
		"sessionId", sess.ID,
		"clusterHash", req.ClusterHash,
		"context", req.Context,
		"port", assignedPort,
	)

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
	args = append(args, "--port", strconv.Itoa(assignedPort))

	cmd := exec.Command(kubectlPath, args...)
	cmd.Env = env.GetShellEnvironment()

	// Log the exact command being executed
	slog.Info("Executing kubectl proxy command",
		"command", kubectlPath,
		"args", args,
		"port", assignedPort,
		"context", req.Context,
	)

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

		slog.Info("Using custom kubeconfig for proxy",
			"sessionId", sess.ID,
			"kubeconfigFile", tmpFile,
			"context", req.Context,
		)
	} else {
		slog.Info("Using default kubeconfig for proxy",
			"sessionId", sess.ID,
			"context", req.Context,
		)
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
		// CRITICAL: Clean up temp files AFTER kubectl finishes
		// This ensures kubectl can read the kubeconfig file for the entire duration
		defer func() {
			for _, tmpFile := range sess.TempFiles {
				if err := os.Remove(tmpFile); err != nil && !os.IsNotExist(err) {
					slog.Warn("Failed to remove temp file", "file", tmpFile, "error", err)
				} else {
					slog.Debug("Removed temp file after proxy completed", "file", tmpFile)
				}
			}
			// Clear the list so session cleanup doesn't try to delete them again
			sess.TempFiles = nil
		}()

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
			slog.Error("kubectl proxy exited immediately", "port", assignedPort, "context", req.Context)
			http.Error(w, "kubectl proxy failed to start (process exited)", http.StatusInternalServerError)
			return
		}

		// Try to connect to the proxy port
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", assignedPort), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			proxyReady = true
			break
		}
	}

	if !proxyReady {
		h.sessionMgr.Stop(sess.ID)
		slog.Error("kubectl proxy did not start listening", "port", assignedPort, "context", req.Context)
		http.Error(w, "kubectl proxy failed to start listening on port", http.StatusInternalServerError)
		return
	}

	slog.Info("Proxy started and verified", "id", sess.ID, "port", assignedPort, "context", req.Context)

	response := ProxyStartResponse{
		SessionID:   sess.ID,
		Port:        assignedPort,
		ClusterHash: req.ClusterHash,
		Status:      string(sess.Status),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Stop handles DELETE /proxy/stop/{sessionId}
func (h *ProxyHandler) Stop(w http.ResponseWriter, r *http.Request) {
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

// Verify handles GET /proxy/verify/{clusterHash}
// Returns information about which cluster a hash belongs to
// This allows the app to verify it's talking to the right cluster
func (h *ProxyHandler) Verify(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterHash := vars["clusterHash"]

	// Find proxy session for this hash
	proxies := h.sessionMgr.FindByClusterHash(clusterHash)
	var proxySession *session.Session
	for _, sess := range proxies {
		if sess.Type == session.TypeProxy && sess.Status == session.StatusRunning {
			if sess.ClusterHash == clusterHash {
				proxySession = sess
				break
			}
		}
	}

	if proxySession == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"found":       false,
			"clusterHash": clusterHash,
			"error":       "No running proxy found for this cluster hash",
		})
		return
	}

	// Return detailed information about the cluster
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"found":       true,
		"clusterHash": proxySession.ClusterHash,
		"context":     proxySession.Context,
		"port":        proxySession.Port,
		"sessionId":   proxySession.ID,
		"status":      string(proxySession.Status),
		"startedAt":   proxySession.StartedAt.Format(time.RFC3339),
	})
}

// assignPortForCluster assigns a unique port for a cluster hash
// This ensures each cluster gets its own port, preventing cross-cluster contamination
func (h *ProxyHandler) assignPortForCluster(clusterHash string) int {
	// Strategy: Use a deterministic port based on cluster hash
	// This ensures the same cluster always gets the same port (good for caching)
	// Port range: 47824-57823 (10,000 ports available)
	// We start at 47824 (helper is on 47823)

	if clusterHash == "" {
		// Fallback for empty hash (shouldn't happen, but be safe)
		return 8001
	}

	// Convert first 4 characters of hash to a number
	// Hash is hex string, so we can parse it
	var hashNum uint32
	for i := 0; i < 4 && i < len(clusterHash); i++ {
		hashNum = hashNum*16 + uint32(hexCharToInt(clusterHash[i]))
	}

	// Map to port range 47824-57823 (10,000 ports)
	port := 47824 + int(hashNum%10000)

	return port
}

// hexCharToInt converts a hex character to its integer value
func hexCharToInt(c byte) int {
	if c >= '0' && c <= '9' {
		return int(c - '0')
	}
	if c >= 'a' && c <= 'f' {
		return int(c - 'a' + 10)
	}
	if c >= 'A' && c <= 'F' {
		return int(c - 'A' + 10)
	}
	return 0
}
