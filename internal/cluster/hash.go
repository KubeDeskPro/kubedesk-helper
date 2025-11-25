package cluster

import (
	"crypto/sha256"
	"fmt"
)

// ComputeHash computes a deterministic hash for a cluster based on kubeconfig and context
// This hash is used to ensure requests are never routed to the wrong cluster
func ComputeHash(kubeconfig, context string) string {
	// If both are empty, return empty string (no cluster specified)
	if kubeconfig == "" && context == "" {
		return ""
	}

	// Compute SHA256 hash of kubeconfig + context
	data := fmt.Sprintf("%s:%s", kubeconfig, context)
	hash := sha256.Sum256([]byte(data))
	
	// Return first 16 characters of hex encoding (sufficient for uniqueness)
	return fmt.Sprintf("%x", hash)[:16]
}

// ValidateHash validates that the provided hash matches the computed hash
// Returns true if valid, false otherwise
// Returns (isValid, expectedHash) for better error reporting
func ValidateHash(providedHash, kubeconfig, context string) bool {
	expectedHash := ComputeHash(kubeconfig, context)

	// If no hash is provided and none is expected, it's valid (backward compat)
	if providedHash == "" && expectedHash == "" {
		return true
	}

	// Otherwise, hashes must match exactly
	return providedHash == expectedHash
}

// GetExpectedHash returns the expected hash for debugging purposes
func GetExpectedHash(kubeconfig, context string) string {
	return ComputeHash(kubeconfig, context)
}

