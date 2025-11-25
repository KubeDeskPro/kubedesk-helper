package api

import (
	"github.com/gorilla/mux"
	"github.com/kubedeskpro/kubedesk-helper/internal/session"
)

// NewRouter creates and configures the HTTP router
func NewRouter(version string, sessionMgr *session.Manager) *mux.Router {
	r := mux.NewRouter()

	// Create handlers
	healthHandler := &HealthHandler{version: version}
	kubectlHandler := &KubectlHandler{}
	execAuthHandler := &ExecAuthHandler{}
	shellHandler := &ShellHandler{sessionMgr: sessionMgr}
	portForwardHandler := &PortForwardHandler{sessionMgr: sessionMgr}
	execHandler := &ExecHandler{sessionMgr: sessionMgr}
	proxyHandler := &ProxyHandler{sessionMgr: sessionMgr}
	sessionCleanupHandler := NewSessionCleanupHandler(sessionMgr)

	// Existing API endpoints (backward compatibility)
	r.HandleFunc("/health", healthHandler.Handle).Methods("GET")
	r.HandleFunc("/kubectl", kubectlHandler.Handle).Methods("POST")
	r.HandleFunc("/exec-auth", execAuthHandler.Handle).Methods("POST")

	// Shell endpoints
	r.HandleFunc("/shell/start", shellHandler.Start).Methods("POST")
	r.HandleFunc("/shell/output/{sessionId}", shellHandler.Output).Methods("GET")
	r.HandleFunc("/shell/stop/{sessionId}", shellHandler.Stop).Methods("DELETE")
	r.HandleFunc("/shell/list", shellHandler.List).Methods("GET")

	// Port-forward endpoints
	r.HandleFunc("/port-forward/start", portForwardHandler.Start).Methods("POST")
	r.HandleFunc("/port-forward/stop/{sessionId}", portForwardHandler.Stop).Methods("DELETE")
	r.HandleFunc("/port-forward/list", portForwardHandler.List).Methods("GET")

	// Exec session endpoints
	r.HandleFunc("/exec/start", execHandler.Start).Methods("POST")
	r.HandleFunc("/exec/input/{sessionId}", execHandler.Input).Methods("POST")
	r.HandleFunc("/exec/output/{sessionId}", execHandler.Output).Methods("GET")
	r.HandleFunc("/exec/stop/{sessionId}", execHandler.Stop).Methods("DELETE")

	// Proxy endpoints
	r.HandleFunc("/proxy/start", proxyHandler.Start).Methods("POST")
	r.HandleFunc("/proxy/stop/{sessionId}", proxyHandler.Stop).Methods("DELETE")
	r.HandleFunc("/proxy/list", proxyHandler.List).Methods("GET")

	// Proxy router - routes requests to the correct kubectl proxy based on cluster hash
	// This allows the app to make requests through the helper instead of directly to kubectl proxy
	// Pattern: /proxy/{clusterHash}/api/v1/pods -> routes to kubectl proxy for that cluster
	proxyRouterHandler := NewProxyRouterHandler(sessionMgr)
	r.PathPrefix("/proxy/{clusterHash}/").HandlerFunc(proxyRouterHandler.Route)

	// Session cleanup endpoint
	r.HandleFunc("/sessions/cleanup", sessionCleanupHandler.Cleanup).Methods("POST")

	return r
}

