# KubeDesk Helper

A lightweight Go-based helper service for KubeDesk, providing advanced Kubernetes operations that require unsandboxed access.

## Installation

### Via Homebrew (Recommended)

```bash
brew tap kubedeskpro/tap
brew install kubedesk-helper
brew services start kubedesk-helper
```

### Manual Installation

```bash
# Clone the repository
git clone https://github.com/kubedeskpro/kubedesk-helper.git
cd kubedesk-helper

# Build universal binary (amd64 + arm64)
make build-universal

# Install to /usr/local/bin
make install
```

## Usage

The helper runs as a background service on port **47823**.

### Start the Helper

```bash
# Via Homebrew services
brew services start kubedesk-helper

# Or run directly
kubedesk-helper
```

### Stop the Helper

```bash
# Via Homebrew services
brew services stop kubedesk-helper

# Or kill the process
pkill kubedesk-helper
```

## API Endpoints

### Health Check
```bash
GET /health
Response: {"version": "2.0.0", "status": "ok"}
```

### Execute kubectl Command
```bash
POST /kubectl
Request: {
  "args": ["get", "pods", "-n", "default"],
  "kubeconfig": "...",  # optional
  "context": "minikube" # optional
}
Response: {
  "stdout": "...",
  "stderr": "...",
  "exitCode": 0
}
```

### Execute Exec-Auth Command
```bash
POST /exec-auth
Request: {
  "command": "aws",
  "args": ["eks", "get-token", "--cluster-name", "my-cluster"],
  "env": {"AWS_PROFILE": "default"}
}
Response: {
  "stdout": "...",
  "stderr": "...",
  "exitCode": 0
}
```

### Port-Forwarding

#### Start Port-Forward
```bash
POST /port-forward/start
Request: {
  "namespace": "default",
  "resourceType": "service",
  "resourceName": "my-service",
  "servicePort": "8080",
  "localPort": "8080",
  "kubeconfig": "...",
  "context": "minikube"
}
Response: {
  "sessionId": "uuid",
  "status": "running"
}
```

#### Stop Port-Forward
```bash
DELETE /port-forward/stop/{sessionId}
Response: {"status": "stopped"}
```

#### List Port-Forwards
```bash
GET /port-forward/list
Response: {
  "sessions": [...]
}
```

### Exec Sessions

#### Start Exec Session
```bash
POST /exec/start
Request: {
  "namespace": "default",
  "podName": "my-pod",
  "container": "main",
  "command": ["/bin/sh"],
  "kubeconfig": "...",
  "context": "minikube"
}
Response: {
  "sessionId": "uuid",
  "status": "running"
}
```

#### Send Input to Exec Session
```bash
POST /exec/input/{sessionId}
Request: {"input": "ls -la\n"}
Response: {"status": "ok"}
```

#### Read Output from Exec Session
```bash
GET /exec/output/{sessionId}
Response: {
  "output": "...",
  "timestamp": "...",
  "status": "running"
}
```

#### Stop Exec Session
```bash
DELETE /exec/stop/{sessionId}
Response: {"status": "stopped"}
```

### kubectl Proxy

#### Start Proxy
```bash
POST /proxy/start
Request: {
  "port": 8001,
  "kubeconfig": "...",
  "context": "minikube"
}
Response: {
  "sessionId": "uuid",
  "port": 8001,
  "status": "running"
}
```

#### Stop Proxy
```bash
DELETE /proxy/stop/{sessionId}
Response: {"status": "stopped"}
```

#### List Proxies
```bash
GET /proxy/list
Response: {
  "sessions": [...]
}
```

## Development

### Build

```bash
# Build for current architecture
make build

# Build universal binary
make build-universal

# Clean build artifacts
make clean
```

### Test

```bash
make test
```

### Run Locally

```bash
make run
```

## Requirements

- **macOS**: 11.0+ (Big Sur and later)
- **Go**: 1.21+ (for building)
- **kubectl**: Any version (must be in PATH)


