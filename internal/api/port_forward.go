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

// PortForwardHandler handles port-forward endpoints
type PortForwardHandler struct {
	sessionMgr *session.Manager
}

// PortForwardStartRequest represents a port-forward start request
type PortForwardStartRequest struct {
	Namespace    string `json:"namespace"`
	ResourceType string `json:"resourceType"` // "service" or "pod"
	ResourceName string `json:"resourceName"`
	ServicePort  string `json:"servicePort"`
	LocalPort    string `json:"localPort"`
	Kubeconfig   string `json:"kubeconfig,omitempty"`
	Context      string `json:"context,omitempty"`
}

// PortForwardStartResponse represents a port-forward start response
type PortForwardStartResponse struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
}

// PortForwardListResponse represents a port-forward list response
type PortForwardListResponse struct {
	Sessions []PortForwardSessionInfo `json:"sessions"`
}

// PortForwardSessionInfo represents port-forward session information
type PortForwardSessionInfo struct {
	SessionID    string `json:"sessionId"`
	Namespace    string `json:"namespace"`
	ResourceType string `json:"resourceType"`
	ResourceName string `json:"resourceName"`
	ServicePort  string `json:"servicePort"`
	LocalPort    string `json:"localPort"`
	Status       string `json:"status"`
	StartedAt    string `json:"startedAt"`
}

// Start handles POST /port-forward/start
func (h *PortForwardHandler) Start(w http.ResponseWriter, r *http.Request) {
	var req PortForwardStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode port-forward request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate request
	if req.Namespace == "" || req.ResourceName == "" || req.ServicePort == "" || req.LocalPort == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	if req.ResourceType != "service" && req.ResourceType != "pod" {
		req.ResourceType = "pod" // Default to pod
	}

	// Create session
	sess := h.sessionMgr.Create(session.TypePortForward)
	sess.Namespace = req.Namespace
	sess.ResourceType = req.ResourceType
	sess.ResourceName = req.ResourceName
	sess.ServicePort = req.ServicePort
	sess.LocalPort = req.LocalPort
	sess.Context = req.Context
	sess.Kubeconfig = req.Kubeconfig

	// Find kubectl
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		h.sessionMgr.Stop(sess.ID)
		http.Error(w, "kubectl not found in PATH", http.StatusInternalServerError)
		return
	}

	// Build kubectl port-forward command
	args := []string{"port-forward"}
	if req.Context != "" {
		args = append(args, "--context", req.Context)
	}
	args = append(args, "-n", req.Namespace)
	
	resource := fmt.Sprintf("%s/%s", req.ResourceType, req.ResourceName)
	args = append(args, resource, fmt.Sprintf("%s:%s", req.LocalPort, req.ServicePort))

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

	// Start port-forward in background
	if err := cmd.Start(); err != nil {
		h.sessionMgr.Stop(sess.ID)
		slog.Error("Failed to start port-forward", "error", err)
		http.Error(w, fmt.Sprintf("Failed to start port-forward: %v", err), http.StatusInternalServerError)
		return
	}

	// Monitor process in background
	go func() {
		cmd.Wait()
		sess.Status = session.StatusStopped
		slog.Info("Port-forward session ended", "id", sess.ID)
	}()

	slog.Info("Port-forward started", "id", sess.ID, "resource", resource, "ports", fmt.Sprintf("%s:%s", req.LocalPort, req.ServicePort))

	response := PortForwardStartResponse{
		SessionID: sess.ID,
		Status:    string(sess.Status),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Stop handles DELETE /port-forward/stop/{sessionId}
func (h *PortForwardHandler) Stop(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sessionID := vars["sessionId"]

	if err := h.sessionMgr.Stop(sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

// List handles GET /port-forward/list
func (h *PortForwardHandler) List(w http.ResponseWriter, r *http.Request) {
	sessions := h.sessionMgr.List(session.TypePortForward)

	var sessionInfos []PortForwardSessionInfo
	for _, sess := range sessions {
		sessionInfos = append(sessionInfos, PortForwardSessionInfo{
			SessionID:    sess.ID,
			Namespace:    sess.Namespace,
			ResourceType: sess.ResourceType,
			ResourceName: sess.ResourceName,
			ServicePort:  sess.ServicePort,
			LocalPort:    sess.LocalPort,
			Status:       string(sess.Status),
			StartedAt:    sess.StartedAt.Format(time.RFC3339),
		})
	}

	response := PortForwardListResponse{Sessions: sessionInfos}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

