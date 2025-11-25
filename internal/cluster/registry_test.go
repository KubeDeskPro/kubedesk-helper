package cluster

import (
	"testing"
)

func TestRegistry_RegisterAndLookup(t *testing.T) {
	registry := &Registry{
		clusters: make(map[string]ClusterInfo),
	}

	// Test 1: Register and lookup
	hash := "abc123def456"
	kubeconfig := "/path/to/kubeconfig"
	context := "my-cluster"

	registry.Register(hash, kubeconfig, context)

	gotKubeconfig, gotContext, found := registry.Lookup(hash)
	if !found {
		t.Errorf("Expected to find hash %s, but it was not found", hash)
	}
	if gotKubeconfig != kubeconfig {
		t.Errorf("Expected kubeconfig %s, got %s", kubeconfig, gotKubeconfig)
	}
	if gotContext != context {
		t.Errorf("Expected context %s, got %s", context, gotContext)
	}

	// Test 2: Lookup non-existent hash
	_, _, found = registry.Lookup("nonexistent")
	if found {
		t.Errorf("Expected not to find nonexistent hash")
	}

	// Test 3: Empty hash
	registry.Register("", kubeconfig, context)
	_, _, found = registry.Lookup("")
	if found {
		t.Errorf("Expected not to find empty hash")
	}
}

func TestComputeAndRegister(t *testing.T) {
	// Reset global registry
	globalRegistry = &Registry{
		clusters: make(map[string]ClusterInfo),
	}

	kubeconfig := "/path/to/kubeconfig"
	context := "my-cluster"

	// Compute and register
	hash := ComputeAndRegister(kubeconfig, context)

	if hash == "" {
		t.Errorf("Expected non-empty hash")
	}

	// Verify it was registered
	gotKubeconfig, gotContext, found := globalRegistry.Lookup(hash)
	if !found {
		t.Errorf("Expected hash to be registered")
	}
	if gotKubeconfig != kubeconfig {
		t.Errorf("Expected kubeconfig %s, got %s", kubeconfig, gotKubeconfig)
	}
	if gotContext != context {
		t.Errorf("Expected context %s, got %s", context, gotContext)
	}
}

func TestValidateAndLookup(t *testing.T) {
	// Reset global registry
	globalRegistry = &Registry{
		clusters: make(map[string]ClusterInfo),
	}

	kubeconfig := "/path/to/kubeconfig"
	context := "my-cluster"
	hash := ComputeAndRegister(kubeconfig, context)

	tests := []struct {
		name               string
		providedHash       string
		providedKubeconfig string
		providedContext    string
		expectValid        bool
		expectKubeconfig   string
		expectContext      string
	}{
		{
			name:               "Valid with kubeconfig and context provided",
			providedHash:       hash,
			providedKubeconfig: kubeconfig,
			providedContext:    context,
			expectValid:        true,
			expectKubeconfig:   kubeconfig,
			expectContext:      context,
		},
		{
			name:               "Valid with only hash (lookup from registry)",
			providedHash:       hash,
			providedKubeconfig: "",
			providedContext:    "",
			expectValid:        true,
			expectKubeconfig:   kubeconfig,
			expectContext:      context,
		},
		{
			name:               "Invalid hash with kubeconfig and context",
			providedHash:       "wronghash",
			providedKubeconfig: kubeconfig,
			providedContext:    context,
			expectValid:        false,
			expectKubeconfig:   kubeconfig,
			expectContext:      context,
		},
		{
			name:               "Invalid hash not in registry",
			providedHash:       "wronghash",
			providedKubeconfig: "",
			providedContext:    "",
			expectValid:        false,
			expectKubeconfig:   "",
			expectContext:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKubeconfig, gotContext, gotValid := ValidateAndLookup(tt.providedHash, tt.providedKubeconfig, tt.providedContext)

			if gotValid != tt.expectValid {
				t.Errorf("Expected valid=%v, got %v", tt.expectValid, gotValid)
			}
			if gotKubeconfig != tt.expectKubeconfig {
				t.Errorf("Expected kubeconfig %s, got %s", tt.expectKubeconfig, gotKubeconfig)
			}
			if gotContext != tt.expectContext {
				t.Errorf("Expected context %s, got %s", tt.expectContext, gotContext)
			}
		})
	}
}

