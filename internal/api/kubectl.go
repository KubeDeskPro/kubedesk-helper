package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/kubedeskpro/kubedesk-helper/internal/cluster"
	"github.com/kubedeskpro/kubedesk-helper/internal/kubectl"
)

// KubectlHandler handles /kubectl endpoint
type KubectlHandler struct{}

// KubectlRequest represents a kubectl command request
type KubectlRequest struct {
	Args        []string `json:"args"`
	Kubeconfig  string   `json:"kubeconfig,omitempty"`
	Context     string   `json:"context,omitempty"`
	ClusterHash string   `json:"clusterHash,omitempty"` // Optional: computed by helper if not provided
}

// KubectlResponse represents a kubectl command response
type KubectlResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int32  `json:"exitCode"`
}

// Handle processes kubectl command requests
func (h *KubectlHandler) Handle(w http.ResponseWriter, r *http.Request) {
	var req KubectlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Error("Failed to decode kubectl request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.Args) == 0 {
		http.Error(w, "No kubectl arguments provided", http.StatusBadRequest)
		return
	}

	// Compute cluster hash if not provided
	if req.ClusterHash == "" {
		req.ClusterHash = cluster.ComputeHash(req.Kubeconfig, req.Context)
	}

	// Validate cluster hash
	if !cluster.ValidateHash(req.ClusterHash, req.Kubeconfig, req.Context) {
		slog.Error("Cluster hash validation failed",
			"providedHash", req.ClusterHash,
			"args", req.Args,
		)
		http.Error(w, "Cluster hash validation failed", http.StatusBadRequest)
		return
	}

	slog.Debug("kubectl request", "args", req.Args, "clusterHash", req.ClusterHash)

	// Execute kubectl command with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := kubectl.Execute(ctx, req.Args, req.Kubeconfig, req.Context)
	if err != nil {
		slog.Error("Failed to execute kubectl", "error", err, "args", req.Args)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := KubectlResponse{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

