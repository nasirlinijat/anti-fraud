package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/nasirlinijat/anti-fraud/backend/internal/services/simulation"
)

// shutDownOnSignal waits for the signal context to end, then drains the
// running simulation and shuts the server down. The returned channel is closed
// when that is done.
func shutDownOnSignal(signalCtx context.Context, server *http.Server, manager *simulation.Service) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-signalCtx.Done()
		if manager != nil {
			drain, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			if err := manager.Close(drain); err != nil {
				slog.Error("drain simulation", "error", err)
			}
			cancel()
		}
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			slog.Error("shutdown", "error", err)
			_ = server.Close()
		}
	}()
	return done
}
