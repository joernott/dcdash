// SPDX-License-Identifier: GPL-3.0-or-later

// Package server implements the dcdash HTTP(S) server: routing, middleware,
// health endpoints and graceful shutdown.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/version"
)

// Options holds optional collaborators of the server.
type Options struct {
	// UI serves the frontend for all non-API paths. If nil, such paths return 404.
	UI http.Handler
	// Checks are evaluated by /readyz.
	Checks []Check
}

// Server is the dcdash HTTP(S) server.
type Server struct {
	cfg     *config.Config
	log     zerolog.Logger
	opts    Options
	handler http.Handler
}

// New creates a server. It does not start listening.
func New(cfg *config.Config, log zerolog.Logger, opts Options) *Server {
	s := &Server{cfg: cfg, log: log, opts: opts}
	s.handler = s.routes()
	return s
}

// Handler returns the complete HTTP handler including middleware.
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /api/v1/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, version.Get())
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such API endpoint")
	})
	if s.opts.UI != nil {
		mux.Handle("/", s.opts.UI)
	}
	return chain(mux, s.log)
}

// Run listens on the configured address and serves until ctx is canceled,
// then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return s.Serve(ctx, ln)
}

// Serve serves on ln until ctx is canceled, then shuts down gracefully.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	hs := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: s.cfg.Server.ReadHeaderTimeout.D(),
		ErrorLog:          stdLogger(s.log),
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	useTLS := s.cfg.Server.TLS.Mode == config.TLSModeFiles
	if useTLS {
		cert, err := tls.LoadX509KeyPair(s.cfg.Server.TLS.CertFile, s.cfg.Server.TLS.KeyFile)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("load TLS certificate: %w", err)
		}
		hs.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	}

	errCh := make(chan error, 1)
	go func() {
		s.log.Info().Str("addr", ln.Addr().String()).Bool("tls", useTLS).Msg("server listening")
		if useTLS {
			errCh <- hs.ServeTLS(ln, "", "")
		} else {
			errCh <- hs.Serve(ln)
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	s.log.Info().Msg("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.Server.ShutdownTimeout.D())
	defer cancel()
	if err := hs.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	s.log.Info().Msg("server stopped")
	return nil
}
