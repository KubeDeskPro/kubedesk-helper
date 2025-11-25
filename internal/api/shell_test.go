package api

import (
	"testing"
)

func TestInjectKubectlContext(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		context  string
		expected string
	}{
		{
			name:     "Simple kubectl command",
			command:  "kubectl get pods",
			context:  "minikube",
			expected: "kubectl --context=minikube get pods",
		},
		{
			name:     "kubectl with namespace",
			command:  "kubectl get pods -n default",
			context:  "prod",
			expected: "kubectl --context=prod get pods -n default",
		},
		{
			name:     "Chained kubectl commands with &&",
			command:  "kubectl get pods && kubectl get svc",
			context:  "minikube",
			expected: "kubectl --context=minikube get pods && kubectl --context=minikube get svc",
		},
		{
			name:     "Mixed commands",
			command:  "echo hello && kubectl get pods && ls -la",
			context:  "minikube",
			expected: "echo hello && kubectl --context=minikube get pods && ls -la",
		},
		{
			name:     "kubectl with pipe",
			command:  "kubectl get pods | grep nginx",
			context:  "minikube",
			expected: "kubectl --context=minikube get pods | grep nginx",
		},
		{
			name:     "Multiple pipes",
			command:  "kubectl get pods | grep nginx | wc -l",
			context:  "minikube",
			expected: "kubectl --context=minikube get pods | grep nginx | wc -l",
		},
		{
			name:     "kubectl with semicolon",
			command:  "kubectl get pods; kubectl get svc",
			context:  "minikube",
			expected: "kubectl --context=minikube get pods; kubectl --context=minikube get svc",
		},
		{
			name:     "kubectl with OR operator",
			command:  "kubectl get pods || echo failed",
			context:  "minikube",
			expected: "kubectl --context=minikube get pods || echo failed",
		},
		{
			name:     "Already has context flag - should skip",
			command:  "kubectl --context=existing get pods",
			context:  "minikube",
			expected: "kubectl --context=existing get pods",
		},
		{
			name:     "Mixed: one has context, one doesn't - skips all if any has context",
			command:  "kubectl --context=foo get pods && kubectl get svc",
			context:  "minikube",
			expected: "kubectl --context=foo get pods && kubectl get svc", // Skips injection if --context found anywhere
		},
		{
			name:     "No kubectl command",
			command:  "echo hello && ls -la",
			context:  "minikube",
			expected: "echo hello && ls -la",
		},
		{
			name:     "kubectl in string - limitation of simple regex",
			command:  "echo 'kubectl is great' && ls",
			context:  "minikube",
			expected: "echo 'kubectl --context=minikube is great' && ls", // Known limitation - acceptable for real use
		},
		{
			name:     "Empty context",
			command:  "kubectl get pods",
			context:  "",
			expected: "kubectl get pods",
		},
		{
			name:     "kubectl config current-context",
			command:  "kubectl config current-context",
			context:  "minikube",
			expected: "kubectl --context=minikube config current-context",
		},
		{
			name:     "Complex real-world example",
			command:  "kubectl config current-context && kubectl get pods --all-namespaces --no-headers | wc -l",
			context:  "minikube",
			expected: "kubectl --context=minikube config current-context && kubectl --context=minikube get pods --all-namespaces --no-headers | wc -l",
		},
		{
			name:     "kubectl with tabs",
			command:  "kubectl\tget\tpods",
			context:  "minikube",
			expected: "kubectl --context=minikube\tget\tpods",
		},
		{
			name:     "kubectl with multiple spaces",
			command:  "kubectl  get  pods",
			context:  "minikube",
			expected: "kubectl --context=minikube  get  pods",
		},
		{
			name:     "kubectl with trailing space",
			command:  "kubectl get po ",
			context:  "minikube",
			expected: "kubectl --context=minikube get po ",
		},
		{
			name:     "kubectl with only trailing space",
			command:  "kubectl ",
			context:  "minikube",
			expected: "kubectl --context=minikube ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := injectKubectlContext(tt.command, tt.context)
			if result != tt.expected {
				t.Errorf("injectKubectlContext() failed\nCommand:  %q\nContext:  %q\nExpected: %q\nGot:      %q",
					tt.command, tt.context, tt.expected, result)
			}
		})
	}
}

