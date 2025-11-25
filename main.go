package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kubedeskpro/kubedesk-helper/internal/api"
	"github.com/kubedeskpro/kubedesk-helper/internal/logging"
	"github.com/kubedeskpro/kubedesk-helper/internal/session"
)

// version is set via ldflags during build: -ldflags "-X main.version=x.y.z"
var version = "dev"

const (
	port = 47823
)

func main() {
	// Setup async structured logging for zero-overhead logging
	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	} else if os.Getenv("LOG_LEVEL") == "warn" {
		logLevel = slog.LevelWarn
	}

	// Create async logger with 10000 entry queue
	logger := logging.NewAsyncLogger(os.Stdout, logLevel, 10000)
	slog.SetDefault(logger)

	slog.Info("Starting KubeDesk Helper", "version", version, "port", port, "logLevel", logLevel.String())

	// Create session manager
	sessionMgr := session.NewManager()

	// Create HTTP server
	router := api.NewRouter(version, sessionMgr)
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		slog.Info("Server listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down server...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Stop cleanup goroutine
	sessionMgr.Shutdown()

	// Stop all sessions
	sessionMgr.StopAll()

	// Shutdown HTTP server
	if err := server.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	slog.Info("Server stopped")

	// Flush async logs before exit
	if asyncLogger, ok := slog.Default().Handler().(*logging.AsyncHandler); ok {
		asyncLogger.Close()
	}
}

