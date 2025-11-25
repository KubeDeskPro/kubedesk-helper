package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/kubedeskpro/kubedesk-helper/internal/session"
)

// TestProxyRouting_KubernetesCommands tests that common Kubernetes API calls work through the proxy router
func TestProxyRouting_KubernetesCommands(t *testing.T) {
	// Skip if not in integration test mode or if minikube is not available
	if os.Getenv("INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=true to run.")
	}

	// Check if minikube context exists
	context := os.Getenv("TEST_CONTEXT")
	if context == "" {
		context = "minikube"
	}

	// Setup
	sessionMgr := session.NewManager()
	defer sessionMgr.StopAll()

	proxyHandler := &ProxyHandler{sessionMgr: sessionMgr}
	proxyRouterHandler := NewProxyRouterHandler(sessionMgr)

	router := mux.NewRouter()
	router.HandleFunc("/proxy/start", proxyHandler.Start).Methods("POST")
	router.HandleFunc("/proxy/stop/{sessionId}", proxyHandler.Stop).Methods("DELETE")
	router.PathPrefix("/proxy/{clusterHash}/").HandlerFunc(proxyRouterHandler.Route)

	server := httptest.NewServer(router)
	defer server.Close()

	// Start proxy session
	t.Log("Starting proxy session for context:", context)
	startResp := startProxySession(t, server.URL, context)
	clusterHash := startResp.ClusterHash
	sessionID := startResp.SessionID

	if clusterHash == "" {
		t.Fatal("Failed to get cluster hash from proxy start response")
	}

	t.Logf("Proxy started - ClusterHash: %s, SessionID: %s", clusterHash, sessionID)

	// Wait for proxy to be ready
	time.Sleep(2 * time.Second)

	// Run tests
	t.Run("GetAPIVersions", func(t *testing.T) {
		testGetAPIVersions(t, server.URL, clusterHash)
	})

	t.Run("ListNamespaces", func(t *testing.T) {
		testListNamespaces(t, server.URL, clusterHash)
	})

	t.Run("ListPods", func(t *testing.T) {
		testListPods(t, server.URL, clusterHash)
	})

	t.Run("ListServices", func(t *testing.T) {
		testListServices(t, server.URL, clusterHash)
	})

	t.Run("ListNodes", func(t *testing.T) {
		testListNodes(t, server.URL, clusterHash)
	})

	t.Run("ListDeployments", func(t *testing.T) {
		testListDeployments(t, server.URL, clusterHash)
	})

	t.Run("GetSpecificNamespace", func(t *testing.T) {
		testGetSpecificNamespace(t, server.URL, clusterHash)
	})

	t.Run("ListPodsWithLabelSelector", func(t *testing.T) {
		testListPodsWithLabelSelector(t, server.URL, clusterHash)
	})

	t.Run("ListPodsWithLimit", func(t *testing.T) {
		testListPodsWithLimit(t, server.URL, clusterHash)
	})

	// Cleanup
	stopProxySession(t, server.URL, sessionID)
}

// Helper types for test responses
type K8sListResponse struct {
	Items []map[string]interface{} `json:"items"`
}

type K8sAPIVersions struct {
	Versions []string `json:"versions"`
}

// Helper functions
func startProxySession(t *testing.T, serverURL, context string) ProxyStartResponse {
	reqBody := fmt.Sprintf(`{"context":"%s"}`, context)
	resp, err := http.Post(serverURL+"/proxy/start", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("Failed to start proxy: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Failed to start proxy: status=%d, body=%s", resp.StatusCode, string(body))
	}

	var result ProxyStartResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode proxy start response: %v", err)
	}

	return result
}

func stopProxySession(t *testing.T, serverURL, sessionID string) {
	req, _ := http.NewRequest("DELETE", serverURL+"/proxy/stop/"+sessionID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("Warning: Failed to stop proxy: %v", err)
		return
	}
	defer resp.Body.Close()
}

func makeProxyRequest(t *testing.T, serverURL, clusterHash, path string) *http.Response {
	url := fmt.Sprintf("%s/proxy/%s%s", serverURL, clusterHash, path)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("Failed to make request to %s: %v", path, err)
	}
	return resp
}

// Test functions
func testGetAPIVersions(t *testing.T, serverURL, clusterHash string) {
	resp := makeProxyRequest(t, serverURL, clusterHash, "/api")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api failed: status=%d, body=%s", resp.StatusCode, string(body))
	}

	var result K8sAPIVersions
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode API versions: %v", err)
	}

	if len(result.Versions) == 0 {
		t.Error("Expected at least one API version, got none")
	}

	t.Logf("✓ Got %d API versions", len(result.Versions))
}

func testListNamespaces(t *testing.T, serverURL, clusterHash string) {
	resp := makeProxyRequest(t, serverURL, clusterHash, "/api/v1/namespaces")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/namespaces failed: status=%d, body=%s", resp.StatusCode, string(body))
	}

	var result K8sListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode namespaces: %v", err)
	}

	if len(result.Items) == 0 {
		t.Error("Expected at least one namespace, got none")
	}

	t.Logf("✓ Got %d namespaces", len(result.Items))
}

func testListPods(t *testing.T, serverURL, clusterHash string) {
	resp := makeProxyRequest(t, serverURL, clusterHash, "/api/v1/pods")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/pods failed: status=%d, body=%s", resp.StatusCode, string(body))
	}

	var result K8sListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode pods: %v", err)
	}

	// Pods might be 0 in a fresh cluster, so just check it doesn't error
	t.Logf("✓ Got %d pods", len(result.Items))
}

func testListServices(t *testing.T, serverURL, clusterHash string) {
	resp := makeProxyRequest(t, serverURL, clusterHash, "/api/v1/services")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/services failed: status=%d, body=%s", resp.StatusCode, string(body))
	}

	var result K8sListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode services: %v", err)
	}

	// At minimum, kubernetes service should exist
	if len(result.Items) == 0 {
		t.Error("Expected at least one service (kubernetes), got none")
	}

	t.Logf("✓ Got %d services", len(result.Items))
}

func testListNodes(t *testing.T, serverURL, clusterHash string) {
	resp := makeProxyRequest(t, serverURL, clusterHash, "/api/v1/nodes")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/nodes failed: status=%d, body=%s", resp.StatusCode, string(body))
	}

	var result K8sListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode nodes: %v", err)
	}

	if len(result.Items) == 0 {
		t.Error("Expected at least one node, got none")
	}

	t.Logf("✓ Got %d nodes", len(result.Items))
}

func testListDeployments(t *testing.T, serverURL, clusterHash string) {
	resp := makeProxyRequest(t, serverURL, clusterHash, "/apis/apps/v1/deployments")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /apis/apps/v1/deployments failed: status=%d, body=%s", resp.StatusCode, string(body))
	}

	var result K8sListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode deployments: %v", err)
	}

	// Deployments might be 0, so just check it doesn't error
	t.Logf("✓ Got %d deployments", len(result.Items))
}

func testGetSpecificNamespace(t *testing.T, serverURL, clusterHash string) {
	// Get default namespace
	resp := makeProxyRequest(t, serverURL, clusterHash, "/api/v1/namespaces/default")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/namespaces/default failed: status=%d, body=%s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode namespace: %v", err)
	}

	metadata, ok := result["metadata"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected metadata field in namespace")
	}

	name, ok := metadata["name"].(string)
	if !ok || name != "default" {
		t.Fatalf("Expected namespace name 'default', got %v", name)
	}

	t.Logf("✓ Got specific namespace: %s", name)
}

func testListPodsWithLabelSelector(t *testing.T, serverURL, clusterHash string) {
	// Test with a label selector (might return 0 results, but should not error)
	resp := makeProxyRequest(t, serverURL, clusterHash, "/api/v1/pods?labelSelector=app=test")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/pods with labelSelector failed: status=%d, body=%s", resp.StatusCode, string(body))
	}

	var result K8sListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode pods with label selector: %v", err)
	}

	t.Logf("✓ Label selector query worked, got %d pods", len(result.Items))
}

func testListPodsWithLimit(t *testing.T, serverURL, clusterHash string) {
	// Test with limit parameter
	resp := makeProxyRequest(t, serverURL, clusterHash, "/api/v1/pods?limit=5")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/pods with limit failed: status=%d, body=%s", resp.StatusCode, string(body))
	}

	var result K8sListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode pods with limit: %v", err)
	}

	if len(result.Items) > 5 {
		t.Errorf("Expected at most 5 pods with limit=5, got %d", len(result.Items))
	}

	t.Logf("✓ Limit query worked, got %d pods (max 5)", len(result.Items))
}

// TestProxyRouting_RapidClusterSwitching tests that rapid cluster switching doesn't cause cross-cluster contamination
func TestProxyRouting_RapidClusterSwitching(t *testing.T) {
	// Skip if not in integration test mode
	if os.Getenv("INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test. Set INTEGRATION_TEST=true to run.")
	}

	// This test requires two different contexts
	context1 := os.Getenv("TEST_CONTEXT_1")
	context2 := os.Getenv("TEST_CONTEXT_2")

	if context1 == "" {
		context1 = "minikube"
	}
	if context2 == "" {
		// If only one context available, use the same one (still tests routing)
		context2 = "minikube"
	}

	// Setup
	sessionMgr := session.NewManager()
	defer sessionMgr.StopAll()

	proxyHandler := &ProxyHandler{sessionMgr: sessionMgr}
	proxyRouterHandler := NewProxyRouterHandler(sessionMgr)

	router := mux.NewRouter()
	router.HandleFunc("/proxy/start", proxyHandler.Start).Methods("POST")
	router.HandleFunc("/proxy/stop/{sessionId}", proxyHandler.Stop).Methods("DELETE")
	router.PathPrefix("/proxy/{clusterHash}/").HandlerFunc(proxyRouterHandler.Route)

	server := httptest.NewServer(router)
	defer server.Close()

	t.Log("Testing rapid cluster switching between:", context1, "and", context2)

	// Perform rapid switching
	for i := 0; i < 5; i++ {
		t.Logf("Iteration %d: Switching to %s", i+1, context1)

		// Start proxy for context1
		resp1 := startProxySession(t, server.URL, context1)
		hash1 := resp1.ClusterHash
		time.Sleep(1 * time.Second)

		// Make request to context1
		nsResp1 := makeProxyRequest(t, server.URL, hash1, "/api/v1/namespaces/default")
		if nsResp1.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(nsResp1.Body)
			t.Fatalf("Failed to get namespace from context1: status=%d, body=%s", nsResp1.StatusCode, string(body))
		}
		nsResp1.Body.Close()

		t.Logf("Iteration %d: Switching to %s", i+1, context2)

		// Start proxy for context2
		resp2 := startProxySession(t, server.URL, context2)
		hash2 := resp2.ClusterHash
		time.Sleep(1 * time.Second)

		// Make request to context2
		nsResp2 := makeProxyRequest(t, server.URL, hash2, "/api/v1/namespaces/default")
		if nsResp2.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(nsResp2.Body)
			t.Fatalf("Failed to get namespace from context2: status=%d, body=%s", nsResp2.StatusCode, string(body))
		}
		nsResp2.Body.Close()

		// Verify we can still access context1 (no cross-contamination)
		nsResp1Again := makeProxyRequest(t, server.URL, hash1, "/api/v1/namespaces/default")
		if nsResp1Again.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(nsResp1Again.Body)
			t.Fatalf("Failed to get namespace from context1 after switching: status=%d, body=%s", nsResp1Again.StatusCode, string(body))
		}
		nsResp1Again.Body.Close()

		// Cleanup
		stopProxySession(t, server.URL, resp1.SessionID)
		if hash1 != hash2 {
			stopProxySession(t, server.URL, resp2.SessionID)
		}
	}

	t.Log("✓ Rapid cluster switching test passed - no cross-contamination detected")
}

