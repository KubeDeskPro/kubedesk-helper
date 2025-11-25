package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/kubedeskpro/kubedesk-helper/internal/session"
)

// TestShellClusterIsolation tests that shell commands respect cluster context
func TestShellClusterIsolation(t *testing.T) {
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
		context2 = "Prod-Cluster"
	}

	// Setup
	sessionMgr := session.NewManager()
	defer sessionMgr.StopAll()

	shellHandler := &ShellHandler{sessionMgr: sessionMgr}

	router := mux.NewRouter()
	router.HandleFunc("/shell/start", shellHandler.Start).Methods("POST")
	router.HandleFunc("/shell/output/{sessionId}", shellHandler.Output).Methods("GET")
	router.HandleFunc("/shell/stop/{sessionId}", shellHandler.Stop).Methods("DELETE")

	server := httptest.NewServer(router)
	defer server.Close()

	t.Logf("Testing shell cluster isolation between: %s and %s", context1, context2)

	// Test 1: Run kubectl get pods on context1
	t.Run("Context1_GetPods", func(t *testing.T) {
		sessionID := startShellCommand(t, server.URL, context1, "kubectl get pods --all-namespaces --no-headers | wc -l")
		time.Sleep(2 * time.Second) // Wait for command to complete

		output := getShellOutput(t, server.URL, sessionID)
		t.Logf("Context1 (%s) pod count output: %s", context1, strings.TrimSpace(output))

		stopShellSession(t, server.URL, sessionID)
	})

	// Test 2: Run kubectl get pods on context2
	t.Run("Context2_GetPods", func(t *testing.T) {
		sessionID := startShellCommand(t, server.URL, context2, "kubectl get pods --all-namespaces --no-headers | wc -l")
		time.Sleep(2 * time.Second) // Wait for command to complete

		output := getShellOutput(t, server.URL, sessionID)
		t.Logf("Context2 (%s) pod count output: %s", context2, strings.TrimSpace(output))

		stopShellSession(t, server.URL, sessionID)
	})

	// Test 3: Rapid switching - verify pod counts are different and consistent
	t.Run("RapidSwitching", func(t *testing.T) {
		var context1PodCount, context2PodCount string

		for i := 0; i < 3; i++ {
			t.Logf("Iteration %d: Testing %s", i+1, context1)
			sessionID1 := startShellCommand(t, server.URL, context1, "kubectl get pods --all-namespaces --no-headers | wc -l")
			time.Sleep(2 * time.Second)
			output1 := strings.TrimSpace(getShellOutput(t, server.URL, sessionID1))
			t.Logf("  %s pod count: %s", context1, output1)

			if i == 0 {
				context1PodCount = output1
			} else {
				// Verify consistent pod count for same cluster
				if output1 != context1PodCount {
					t.Logf("  WARNING: Pod count changed for %s (expected %s, got %s) - cluster may have changed",
						context1, context1PodCount, output1)
				}
			}
			stopShellSession(t, server.URL, sessionID1)

			t.Logf("Iteration %d: Testing %s", i+1, context2)
			sessionID2 := startShellCommand(t, server.URL, context2, "kubectl get pods --all-namespaces --no-headers | wc -l")
			time.Sleep(2 * time.Second)
			output2 := strings.TrimSpace(getShellOutput(t, server.URL, sessionID2))
			t.Logf("  %s pod count: %s", context2, output2)

			if i == 0 {
				context2PodCount = output2
			} else {
				// Verify consistent pod count for same cluster
				if output2 != context2PodCount {
					t.Logf("  WARNING: Pod count changed for %s (expected %s, got %s) - cluster may have changed",
						context2, context2PodCount, output2)
				}
			}
			stopShellSession(t, server.URL, sessionID2)

			// Verify context1 still returns correct pod count after switching
			sessionID1Again := startShellCommand(t, server.URL, context1, "kubectl get pods --all-namespaces --no-headers | wc -l")
			time.Sleep(2 * time.Second)
			output1Again := strings.TrimSpace(getShellOutput(t, server.URL, sessionID1Again))
			t.Logf("  %s pod count (after switch): %s", context1, output1Again)

			// This is the key test: after switching to context2 and back, context1 should still return its pod count
			if output1Again != context1PodCount {
				t.Errorf("CLUSTER ISOLATION BUG: After switching, %s returned %s pods but expected %s pods",
					context1, output1Again, context1PodCount)
			}

			stopShellSession(t, server.URL, sessionID1Again)
		}

		// Final verification: the two clusters should have different pod counts
		if context1PodCount == context2PodCount {
			t.Logf("WARNING: Both clusters have same pod count (%s) - test may not be conclusive", context1PodCount)
		} else {
			t.Logf("✓ Cluster isolation verified: %s has %s pods, %s has %s pods",
				context1, context1PodCount, context2, context2PodCount)
		}
	})
}

// Helper functions
func startShellCommand(t *testing.T, serverURL, context, command string) string {
	reqBody := strings.NewReader(`{"context":"` + context + `","command":"` + command + `"}`)
	resp, err := http.Post(serverURL+"/shell/start", "application/json", reqBody)
	if err != nil {
		t.Fatalf("Failed to start shell command: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to start shell command: status=%d", resp.StatusCode)
	}

	var result ShellStartResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	return result.SessionID
}

func getShellOutput(t *testing.T, serverURL, sessionID string) string {
	resp, err := http.Get(serverURL + "/shell/output/" + sessionID)
	if err != nil {
		t.Fatalf("Failed to get shell output: %v", err)
	}
	defer resp.Body.Close()

	var result ShellOutputResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode output: %v", err)
	}

	return result.Output
}

func stopShellSession(t *testing.T, serverURL, sessionID string) {
	req, _ := http.NewRequest("DELETE", serverURL+"/shell/stop/"+sessionID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to stop shell session: %v", err)
	}
	defer resp.Body.Close()
}

