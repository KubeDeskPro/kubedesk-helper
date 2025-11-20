package api

import (
	"encoding/json"
	"net/http"
)

// HealthHandler handles /health endpoint
type HealthHandler struct {
	version string
}

// HealthResponse represents the health check response
type HealthResponse struct {
	Version string `json:"version"`
	Status  string `json:"status"`
}

// Handle processes health check requests
func (h *HealthHandler) Handle(w http.ResponseWriter, r *http.Request) {
	response := HealthResponse{
		Version: h.version,
		Status:  "ok",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

