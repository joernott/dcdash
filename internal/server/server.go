// SPDX-License-Identifier: GPL-3.0-or-later

// Package server implements the dcdash HTTP(S) server: routing, middleware,
// health endpoints, HTTP to HTTPS redirection and graceful shutdown.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/version"
)

// Options holds optional collaborators of the server.
type Options struct {
	// UI serves the frontend for all non-API paths. If nil, such paths return 404.
	UI http.Handler
	// Checks are evaluated by /readyz.
	Checks []Check
	// TLSConfig enables HTTPS. If nil, the server speaks plain HTTP.
	TLSConfig *tls.Config
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
		httpx.WriteJSON(w, http.StatusOK, version.Get())
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "no such API endpoint")
	})
	if s.opts.UI != nil {
		mux.Handle("/", s.opts.UI)
	}
	return chain(mux, s.log, hstsValue(s.cfg.Server.HSTSMaxAge))
}

func hstsValue(maxAge config.Duration) string {
	if maxAge <= 0 {
		return ""
	}
	return "max-age=" + strconv.FormatInt(int64(maxAge.D().Seconds()), 10)
}

// Run listens on the configured addresses and serves until ctx is canceled,
// then shuts down gracefully. With TLS and server.redirect_listen set, a
// second plain HTTP listener redirects to HTTPS.
func (s *Server) Run(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	var redirectLn net.Listener
	if s.opts.TLSConfig != nil && s.cfg.Server.RedirectListen != "" {
		redirectLn, err = lc.Listen(ctx, "tcp", s.cfg.Server.RedirectListen)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("listen for redirects: %w", err)
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return s.Serve(gctx, ln) })
	if redirectLn != nil {
		g.Go(func() error { return s.ServeRedirect(gctx, redirectLn) })
	}
	return g.Wait()
}

// Serve serves the application on ln until ctx is canceled, then shuts down gracefully.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	hs := s.newHTTPServer(ctx, s.handler)
	useTLS := s.opts.TLSConfig != nil
	if useTLS {
		hs.TLSConfig = s.opts.TLSConfig.Clone()
	}
	s.log.Info().Str("addr", ln.Addr().String()).Bool("tls", useTLS).Msg("server listening")
	return s.serve(ctx, hs, func() error {
		if useTLS {
			return hs.ServeTLS(ln, "", "")
		}
		return hs.Serve(ln)
	})
}

// ServeRedirect answers every request on ln with a permanent redirect to the
// same URL on the HTTPS listener.
func (s *Server) ServeRedirect(ctx context.Context, ln net.Listener) error {
	_, port, err := net.SplitHostPort(s.cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("server.listen: %w", err)
	}
	h := chain(redirectHandler(port), s.log, "")
	hs := s.newHTTPServer(ctx, h)
	s.log.Info().Str("addr", ln.Addr().String()).Msg("redirecting HTTP to HTTPS")
	return s.serve(ctx, hs, func() error { return hs.Serve(ln) })
}

func (s *Server) newHTTPServer(ctx context.Context, h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: s.cfg.Server.ReadHeaderTimeout.D(),
		ErrorLog:          stdLogger(s.log),
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
}

// serve runs start until ctx is canceled and then shuts hs down gracefully.
func (s *Server) serve(ctx context.Context, hs *http.Server, start func() error) error {
	errCh := make(chan error, 1)
	go func() { errCh <- start() }()

	select {
	case err := <-errCh:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.Server.ShutdownTimeout.D())
	defer cancel()
	if err := hs.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	s.log.Info().Str("addr", hs.Addr).Msg("listener stopped")
	return nil
}

// redirectHandler redirects to https://<host>[:port]<request URI>. The port is
// omitted when it is the HTTPS default 443.
func redirectHandler(httpsPort string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host == "" || strings.ContainsAny(host, "/\\@ ") {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
		authority := host
		if httpsPort != "443" {
			authority = net.JoinHostPort(host, httpsPort)
		} else if strings.Contains(host, ":") {
			authority = "[" + host + "]" // IPv6 literal
		}
		// #nosec G710 -- same host the client requested, only the scheme (and port) change
		http.Redirect(w, r, "https://"+authority+r.URL.RequestURI(), http.StatusPermanentRedirect)
	})
}
