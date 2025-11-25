package logging

import (
	"context"
	"io"
	"log/slog"
	"sync"
)

// AsyncHandler wraps an slog.Handler and processes logs asynchronously
type AsyncHandler struct {
	handler slog.Handler
	queue   chan *logEntry
	wg      sync.WaitGroup
	closed  bool
	mu      sync.Mutex
}

type logEntry struct {
	ctx    context.Context
	record slog.Record
}

// NewAsyncHandler creates a new async handler with a buffered queue
func NewAsyncHandler(handler slog.Handler, queueSize int) *AsyncHandler {
	if queueSize <= 0 {
		queueSize = 1000 // Default queue size
	}

	h := &AsyncHandler{
		handler: handler,
		queue:   make(chan *logEntry, queueSize),
	}

	// Start background worker
	h.wg.Add(1)
	go h.worker()

	return h
}

// worker processes log entries in the background
func (h *AsyncHandler) worker() {
	defer h.wg.Done()

	for entry := range h.queue {
		// Process log entry (this is where the blocking I/O happens)
		h.handler.Handle(entry.ctx, entry.record)
	}
}

// Handle queues the log record for async processing
func (h *AsyncHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.mu.Unlock()

	// Non-blocking send to queue
	select {
	case h.queue <- &logEntry{ctx: ctx, record: r}:
		// Successfully queued
	default:
		// Queue is full, drop the log (or could block here if you prefer)
		// For maximum performance, we drop rather than block
	}

	return nil
}

// Enabled delegates to the underlying handler
func (h *AsyncHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

// WithAttrs delegates to the underlying handler
func (h *AsyncHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &AsyncHandler{
		handler: h.handler.WithAttrs(attrs),
		queue:   h.queue,
	}
}

// WithGroup delegates to the underlying handler
func (h *AsyncHandler) WithGroup(name string) slog.Handler {
	return &AsyncHandler{
		handler: h.handler.WithGroup(name),
		queue:   h.queue,
	}
}

// Close flushes all pending logs and stops the worker
func (h *AsyncHandler) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	h.mu.Unlock()

	// Close the queue and wait for worker to finish
	close(h.queue)
	h.wg.Wait()
}

// NewAsyncLogger creates a new logger with async JSON handler
func NewAsyncLogger(w io.Writer, level slog.Level, queueSize int) *slog.Logger {
	jsonHandler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
	})

	asyncHandler := NewAsyncHandler(jsonHandler, queueSize)

	return slog.New(asyncHandler)
}

