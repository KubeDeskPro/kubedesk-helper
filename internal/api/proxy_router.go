package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/kubedeskpro/kubedesk-helper/internal/session"
)

// ProxyRouterHandler handles routing requests to the correct kubectl proxy
type ProxyRouterHandler struct {
	sessionMgr *session.Manager
}

// NewProxyRouterHandler creates a new proxy router handler
func NewProxyRouterHandler(sessionMgr *session.Manager) *ProxyRouterHandler {
	return &ProxyRouterHandler{
		sessionMgr: sessionMgr,
	}
}

// Route handles all requests to /proxy/{clusterHash}/*
// It routes the request to the correct kubectl proxy based on the cluster hash
func (h *ProxyRouterHandler) Route(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterHash := vars["clusterHash"]

	// Extract the path after /proxy/{clusterHash}
	// e.g., /proxy/abc123/api/v1/pods -> /api/v1/pods
	prefix := fmt.Sprintf("/proxy/%s", clusterHash)
	targetPath := strings.TrimPrefix(r.URL.Path, prefix)
	if targetPath == "" {
		targetPath = "/"
	}

	slog.Debug("Routing proxy request",
		"clusterHash", clusterHash,
		"path", targetPath,
		"method", r.Method,
	)

	// Find the proxy session for this cluster hash
	proxies := h.sessionMgr.FindByClusterHash(clusterHash)
	var proxySession *session.Session
	for _, sess := range proxies {
		if sess.Type == session.TypeProxy && sess.Status == session.StatusRunning {
			// CRITICAL SAFETY CHECK: Verify cluster hash matches
			if sess.ClusterHash != clusterHash {
				slog.Error("CRITICAL: Found proxy with mismatched cluster hash!",
					"requestedHash", clusterHash,
					"sessionHash", sess.ClusterHash,
					"sessionId", sess.ID,
					"context", sess.Context,
					"port", sess.Port,
				)
				// DO NOT use this proxy - it's for a different cluster!
				continue
			}
			proxySession = sess
			break
		}
	}

	if proxySession == nil {
		slog.Error("No running proxy found for cluster hash - helper may have restarted",
			"clusterHash", clusterHash,
			"path", targetPath,
			"method", r.Method,
		)

		// Return a clear error that tells the app what to do
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		errorResponse := map[string]interface{}{
			"error":       "No proxy running for this cluster",
			"clusterHash": clusterHash,
			"action":      "Call POST /proxy/start with kubeconfig and context to start a new proxy",
			"reason":      "Helper may have restarted and lost session state",
		}
		json.NewEncoder(w).Encode(errorResponse)
		return
	}

	// CRITICAL SAFETY: Double-check cluster hash before forwarding
	if proxySession.ClusterHash != clusterHash {
		slog.Error("CRITICAL SAFETY VIOLATION: Cluster hash mismatch before forwarding!",
			"requestedHash", clusterHash,
			"sessionHash", proxySession.ClusterHash,
			"sessionId", proxySession.ID,
			"context", proxySession.Context,
			"port", proxySession.Port,
			"path", targetPath,
		)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		errorResponse := map[string]interface{}{
			"error":         "CRITICAL: Cluster hash mismatch - refusing to forward request",
			"requestedHash": clusterHash,
			"sessionHash":   proxySession.ClusterHash,
			"reason":        "Safety check failed - this would return data from wrong cluster",
		}
		json.NewEncoder(w).Encode(errorResponse)
		return
	}

	// Build the target URL for the kubectl proxy
	targetURL := fmt.Sprintf("http://localhost:%d%s", proxySession.Port, targetPath)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	slog.Info("Forwarding request to kubectl proxy",
		"clusterHash", clusterHash,
		"context", proxySession.Context,
		"port", proxySession.Port,
		"path", targetPath,
		"method", r.Method,
		"sessionId", proxySession.ID,
	)

	// Create a new request to the kubectl proxy
	proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		slog.Error("Failed to create proxy request", "error", err)
		http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
		return
	}

	// Copy headers from original request
	for key, values := range r.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	// Forward the request to kubectl proxy
	client := &http.Client{}
	resp, err := client.Do(proxyReq)
	if err != nil {
		slog.Error("Failed to forward request to kubectl proxy",
			"error", err,
			"clusterHash", clusterHash,
			"port", proxySession.Port,
		)
		http.Error(w, fmt.Sprintf("Failed to connect to kubectl proxy: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Copy status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		slog.Error("Failed to copy response body", "error", err)
		return
	}
}

