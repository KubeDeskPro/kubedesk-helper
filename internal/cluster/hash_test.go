package cluster

import (
	"testing"
)

func TestComputeHash(t *testing.T) {
	tests := []struct {
		name       string
		kubeconfig string
		context    string
		wantEmpty  bool
	}{
		{
			name:       "empty kubeconfig and context",
			kubeconfig: "",
			context:    "",
			wantEmpty:  true,
		},
		{
			name:       "only kubeconfig",
			kubeconfig: "apiVersion: v1\nkind: Config",
			context:    "",
			wantEmpty:  false,
		},
		{
			name:       "only context",
			kubeconfig: "",
			context:    "prod-cluster",
			wantEmpty:  false,
		},
		{
			name:       "both kubeconfig and context",
			kubeconfig: "apiVersion: v1\nkind: Config",
			context:    "prod-cluster",
			wantEmpty:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := ComputeHash(tt.kubeconfig, tt.context)
			
			if tt.wantEmpty {
				if hash != "" {
					t.Errorf("ComputeHash() = %v, want empty string", hash)
				}
			} else {
				if hash == "" {
					t.Errorf("ComputeHash() = empty, want non-empty hash")
				}
				if len(hash) != 16 {
					t.Errorf("ComputeHash() length = %d, want 16", len(hash))
				}
			}
		})
	}
}

func TestComputeHash_Deterministic(t *testing.T) {
	kubeconfig := "apiVersion: v1\nkind: Config"
	context := "prod-cluster"

	hash1 := ComputeHash(kubeconfig, context)
	hash2 := ComputeHash(kubeconfig, context)

	if hash1 != hash2 {
		t.Errorf("ComputeHash() not deterministic: %v != %v", hash1, hash2)
	}
}

func TestComputeHash_DifferentInputs(t *testing.T) {
	hash1 := ComputeHash("config1", "context1")
	hash2 := ComputeHash("config2", "context1")
	hash3 := ComputeHash("config1", "context2")

	if hash1 == hash2 {
		t.Errorf("Different kubeconfigs produced same hash")
	}
	if hash1 == hash3 {
		t.Errorf("Different contexts produced same hash")
	}
	if hash2 == hash3 {
		t.Errorf("Different inputs produced same hash")
	}
}

func TestValidateHash(t *testing.T) {
	kubeconfig := "apiVersion: v1\nkind: Config"
	context := "prod-cluster"
	validHash := ComputeHash(kubeconfig, context)

	tests := []struct {
		name         string
		providedHash string
		kubeconfig   string
		context      string
		want         bool
	}{
		{
			name:         "valid hash",
			providedHash: validHash,
			kubeconfig:   kubeconfig,
			context:      context,
			want:         true,
		},
		{
			name:         "invalid hash",
			providedHash: "invalid",
			kubeconfig:   kubeconfig,
			context:      context,
			want:         false,
		},
		{
			name:         "empty hash with empty inputs (backward compat)",
			providedHash: "",
			kubeconfig:   "",
			context:      "",
			want:         true,
		},
		{
			name:         "empty hash with non-empty inputs",
			providedHash: "",
			kubeconfig:   kubeconfig,
			context:      context,
			want:         false,
		},
		{
			name:         "hash mismatch - different kubeconfig",
			providedHash: validHash,
			kubeconfig:   "different config",
			context:      context,
			want:         false,
		},
		{
			name:         "hash mismatch - different context",
			providedHash: validHash,
			kubeconfig:   kubeconfig,
			context:      "different-context",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateHash(tt.providedHash, tt.kubeconfig, tt.context)
			if got != tt.want {
				t.Errorf("ValidateHash() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestClusterIsolation ensures that different clusters always get different hashes
// This is CRITICAL for security - we must never route requests to the wrong cluster
func TestClusterIsolation(t *testing.T) {
	prodConfig := "apiVersion: v1\nclusters:\n- name: prod"
	devConfig := "apiVersion: v1\nclusters:\n- name: dev"
	
	prodHash := ComputeHash(prodConfig, "prod")
	devHash := ComputeHash(devConfig, "dev")
	
	if prodHash == devHash {
		t.Fatal("CRITICAL: Different clusters produced same hash! This violates cluster isolation.")
	}
	
	// Verify that using prod hash with dev config fails validation
	if ValidateHash(prodHash, devConfig, "dev") {
		t.Fatal("CRITICAL: Prod hash validated against dev cluster! This violates cluster isolation.")
	}
	
	// Verify that using dev hash with prod config fails validation
	if ValidateHash(devHash, prodConfig, "prod") {
		t.Fatal("CRITICAL: Dev hash validated against prod cluster! This violates cluster isolation.")
	}
}

