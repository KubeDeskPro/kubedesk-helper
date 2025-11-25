package cluster

import (
	"sync"
)

// ClusterInfo stores the kubeconfig and context for a cluster hash
type ClusterInfo struct {
	Kubeconfig string
	Context    string
}

// Registry stores the mapping of cluster hash to cluster info
// This allows us to look up kubeconfig/context from just the hash
type Registry struct {
	mu       sync.RWMutex
	clusters map[string]ClusterInfo
}

// Global registry instance
var globalRegistry = &Registry{
	clusters: make(map[string]ClusterInfo),
}

// GetRegistry returns the global cluster registry
func GetRegistry() *Registry {
	return globalRegistry
}

// Register stores the cluster info for a given hash
func (r *Registry) Register(hash, kubeconfig, context string) {
	if hash == "" {
		return
	}
	
	r.mu.Lock()
	defer r.mu.Unlock()
	
	r.clusters[hash] = ClusterInfo{
		Kubeconfig: kubeconfig,
		Context:    context,
	}
}

// Lookup retrieves the cluster info for a given hash
// Returns (kubeconfig, context, found)
func (r *Registry) Lookup(hash string) (string, string, bool) {
	if hash == "" {
		return "", "", false
	}
	
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	info, found := r.clusters[hash]
	if !found {
		return "", "", false
	}
	
	return info.Kubeconfig, info.Context, true
}

// ComputeAndRegister computes the hash and registers it in one operation
// Returns the computed hash
func ComputeAndRegister(kubeconfig, context string) string {
	hash := ComputeHash(kubeconfig, context)
	if hash != "" {
		globalRegistry.Register(hash, kubeconfig, context)
	}
	return hash
}

// ValidateAndLookup validates a hash and returns the associated cluster info
// If kubeconfig/context are provided, validates against them
// If not provided, looks them up from the registry
// Returns (kubeconfig, context, isValid)
func ValidateAndLookup(providedHash, kubeconfig, context string) (string, string, bool) {
	// If kubeconfig and context are provided, validate directly
	if kubeconfig != "" || context != "" {
		isValid := ValidateHash(providedHash, kubeconfig, context)
		return kubeconfig, context, isValid
	}
	
	// Otherwise, look up from registry
	regKubeconfig, regContext, found := globalRegistry.Lookup(providedHash)
	if !found {
		return "", "", false
	}
	
	// Validate that the hash matches what we have in registry
	isValid := ValidateHash(providedHash, regKubeconfig, regContext)
	return regKubeconfig, regContext, isValid
}

