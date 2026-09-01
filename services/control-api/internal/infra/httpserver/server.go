// Package httpserver owns the Control API process HTTP mechanics.
package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Server wraps net/http lifecycle without owning business routes.
type Server struct {
	server *http.Server
}

// New returns an HTTP server for the already assembled handler.
func New(address string, handler http.Handler) *Server {
	return &Server{server: &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}}
}

// ListenAndServe starts accepting HTTP traffic.
func (s *Server) ListenAndServe() error {
	err := s.server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown drains active requests.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}
