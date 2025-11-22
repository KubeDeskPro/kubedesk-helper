package env

import (
	"bytes"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var (
	cachedEnv     []string
	cachedEnvOnce sync.Once
)

// GetShellEnvironment returns the user's shell environment on macOS
// This ensures we have access to tools installed via Homebrew, gcloud, etc.
// The environment is loaded once and cached for performance.
func GetShellEnvironment() []string {
	cachedEnvOnce.Do(func() {
		// Start with current environment
		baseEnv := os.Environ()

		// Try to get the user's shell environment
		shellEnv := loadShellEnvironment()

		if len(shellEnv) > 0 {
			// Merge shell environment with base environment
			// Shell environment takes precedence for PATH and other important vars
			cachedEnv = mergeEnvironments(baseEnv, shellEnv)
		} else {
			// Fallback to base environment
			cachedEnv = baseEnv
		}

		// Log the PATH for debugging
		for _, e := range cachedEnv {
			if strings.HasPrefix(e, "PATH=") {
				slog.Info("Loaded shell environment", "PATH", e[5:])
				break
			}
		}
	})

	return cachedEnv
}

// loadShellEnvironment loads environment from the user's login shell
func loadShellEnvironment() []string {
	// Get user's shell
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh" // Default to zsh on modern macOS
	}

	// Try interactive login shell first (loads both profile and rc files)
	// This gives us the most complete environment
	// -l: login shell (loads profile files like .zprofile, .bash_profile)
	// -i: interactive shell (loads rc files like .zshrc, .bashrc)
	// -c: execute command
	cmd := exec.Command(shell, "-l", "-i", "-c", "env")

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil // Ignore stderr to avoid noise from shell initialization

	if err := cmd.Run(); err != nil {
		slog.Warn("Failed to load interactive shell environment, trying login shell", "shell", shell, "error", err)

		// Fallback to just login shell
		cmd = exec.Command(shell, "-l", "-c", "env")
		stdout.Reset()
		cmd.Stdout = &stdout
		cmd.Stderr = nil

		if err := cmd.Run(); err != nil {
			slog.Warn("Failed to load shell environment", "shell", shell, "error", err)
			return nil
		}
	}

	// Parse environment variables
	output := stdout.String()
	lines := strings.Split(output, "\n")

	var env []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Only include lines that look like environment variables
		if strings.Contains(line, "=") {
			env = append(env, line)
		}
	}

	slog.Debug("Loaded shell environment", "shell", shell, "vars", len(env))
	return env
}

// mergeEnvironments merges two environment slices
// Variables from shellEnv take precedence over baseEnv
func mergeEnvironments(baseEnv, shellEnv []string) []string {
	// Create a map of shell environment variables
	shellMap := make(map[string]string)
	for _, env := range shellEnv {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			shellMap[parts[0]] = parts[1]
		}
	}
	
	// Create a map of base environment variables
	baseMap := make(map[string]string)
	for _, env := range baseEnv {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			baseMap[parts[0]] = parts[1]
		}
	}
	
	// Important variables that should come from shell environment
	importantVars := []string{
		"PATH",
		"HOME",
		"USER",
		"SHELL",
		"LANG",
		"LC_ALL",
		"KUBECONFIG",
		"GOOGLE_APPLICATION_CREDENTIALS",
		"AWS_PROFILE",
		"AWS_REGION",
		"AWS_DEFAULT_REGION",
	}
	
	// Merge: shell environment takes precedence for important vars
	for _, key := range importantVars {
		if val, ok := shellMap[key]; ok {
			baseMap[key] = val
		}
	}
	
	// Also include any other shell vars that aren't in base
	for key, val := range shellMap {
		if _, exists := baseMap[key]; !exists {
			baseMap[key] = val
		}
	}
	
	// Convert back to slice
	result := make([]string, 0, len(baseMap))
	for key, val := range baseMap {
		result = append(result, key+"="+val)
	}
	
	return result
}

