package api

import (
	"testing"
)

func TestAssignPortForCluster(t *testing.T) {
	handler := &ProxyHandler{}

	tests := []struct {
		name        string
		clusterHash string
		wantPort    int
	}{
		{
			name:        "Empty hash fallback",
			clusterHash: "",
			wantPort:    8001,
		},
		{
			name:        "Hash 1",
			clusterHash: "e40f0908cbe45e0d",
			wantPort:    56207, // Deterministic based on hash
		},
		{
			name:        "Hash 2",
			clusterHash: "03bbba57f539155e",
			wantPort:    48779, // Different from Hash 1
		},
		{
			name:        "Same hash should give same port",
			clusterHash: "e40f0908cbe45e0d",
			wantPort:    56207, // Same as Hash 1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := handler.assignPortForCluster(tt.clusterHash)
			if got != tt.wantPort {
				t.Errorf("assignPortForCluster(%q) = %d, want %d", tt.clusterHash, got, tt.wantPort)
			}
		})
	}
}

func TestAssignPortForCluster_Deterministic(t *testing.T) {
	handler := &ProxyHandler{}
	hash := "abc123def456"

	// Call multiple times - should always return same port
	port1 := handler.assignPortForCluster(hash)
	port2 := handler.assignPortForCluster(hash)
	port3 := handler.assignPortForCluster(hash)

	if port1 != port2 || port2 != port3 {
		t.Errorf("assignPortForCluster not deterministic: got %d, %d, %d", port1, port2, port3)
	}
}

func TestAssignPortForCluster_Range(t *testing.T) {
	handler := &ProxyHandler{}

	// Test various hashes to ensure ports are in valid range
	hashes := []string{
		"0000000000000000",
		"ffffffffffffffff",
		"1234567890abcdef",
		"fedcba0987654321",
		"a1b2c3d4e5f6a7b8",
	}

	for _, hash := range hashes {
		port := handler.assignPortForCluster(hash)
		if port < 47824 || port > 57823 {
			t.Errorf("assignPortForCluster(%q) = %d, want port in range [47824, 57823]", hash, port)
		}
	}
}

func TestAssignPortForCluster_DifferentHashesDifferentPorts(t *testing.T) {
	handler := &ProxyHandler{}

	// Different hashes should (usually) give different ports
	hash1 := "e40f0908cbe45e0d"
	hash2 := "03bbba57f539155e"

	port1 := handler.assignPortForCluster(hash1)
	port2 := handler.assignPortForCluster(hash2)

	if port1 == port2 {
		t.Errorf("Different hashes gave same port: %q=%d, %q=%d", hash1, port1, hash2, port2)
	}
}

func TestHexCharToInt(t *testing.T) {
	tests := []struct {
		char byte
		want int
	}{
		{'0', 0},
		{'1', 1},
		{'9', 9},
		{'a', 10},
		{'f', 15},
		{'A', 10},
		{'F', 15},
		{'g', 0}, // Invalid char
		{'z', 0}, // Invalid char
	}

	for _, tt := range tests {
		t.Run(string(tt.char), func(t *testing.T) {
			got := hexCharToInt(tt.char)
			if got != tt.want {
				t.Errorf("hexCharToInt(%c) = %d, want %d", tt.char, got, tt.want)
			}
		})
	}
}

