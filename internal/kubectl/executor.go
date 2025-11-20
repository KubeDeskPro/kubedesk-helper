package kubectl

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Result represents the result of a kubectl command execution
type Result struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int32  `json:"exitCode"`
}

// Execute runs a kubectl command and returns the result
func Execute(ctx context.Context, args []string, kubeconfig, contextName string) (*Result, error) {
	// Find kubectl binary
	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		return nil, fmt.Errorf("kubectl not found in PATH: %w", err)
	}

	// Build command
	cmd := exec.CommandContext(ctx, kubectlPath, args...)

	// Set environment
	cmd.Env = os.Environ()

	// Set kubeconfig if provided
	if kubeconfig != "" {
		// Write kubeconfig to temp file
		tmpDir := os.TempDir()
		tmpFile := filepath.Join(tmpDir, fmt.Sprintf("kubeconfig-%d", time.Now().UnixNano()))
		if err := os.WriteFile(tmpFile, []byte(kubeconfig), 0600); err != nil {
			return nil, fmt.Errorf("failed to write kubeconfig: %w", err)
		}
		defer os.Remove(tmpFile)
		cmd.Env = append(cmd.Env, fmt.Sprintf("KUBECONFIG=%s", tmpFile))
	}

	// Set context if provided
	if contextName != "" {
		args = append([]string{"--context", contextName}, args...)
		cmd.Args = append([]string{kubectlPath}, args...)
	}

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Debug("Executing kubectl", "args", args)

	// Run command
	err = cmd.Run()

	result := &Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = int32(exitErr.ExitCode())
		} else {
			result.ExitCode = -1
			result.Stderr = err.Error()
		}
	} else {
		result.ExitCode = 0
	}

	slog.Debug("kubectl execution completed", "exitCode", result.ExitCode)
	return result, nil
}

// ExecuteCommand runs an arbitrary command (for exec-auth)
func ExecuteCommand(ctx context.Context, command string, args []string, env map[string]string) (*Result, error) {
	// Find command binary
	cmdPath, err := exec.LookPath(command)
	if err != nil {
		return nil, fmt.Errorf("command not found in PATH: %s: %w", command, err)
	}

	// Build command
	cmd := exec.CommandContext(ctx, cmdPath, args...)

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Debug("Executing command", "command", command, "args", args)

	// Run command
	err = cmd.Run()

	result := &Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = int32(exitErr.ExitCode())
		} else {
			result.ExitCode = -1
			result.Stderr = err.Error()
		}
	} else {
		result.ExitCode = 0
	}

	slog.Debug("Command execution completed", "exitCode", result.ExitCode)
	return result, nil
}

