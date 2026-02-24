package http

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jmoiron/sqlx"

	"github.com/wbrijesh/origin/internal/config"
)

// Server is the HTTP server for the web UI and git protocol.
type Server struct {
	cfg    *config.Config
	db     *sqlx.DB
	server *http.Server
	render *renderer
}

// New creates a new HTTP server with all routes registered.
func New(cfg *config.Config, db *sqlx.DB) *Server {
	s := &Server{
		cfg:    cfg,
		db:     db,
		render: newRenderer(),
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.server = &http.Server{
		Addr:    cfg.HTTP.ListenAddr,
		Handler: s.securityHeaders(s.requestLogger(mux)),
	}

	// Configure TLS if certs are provided
	if cfg.HasTLS() {
		s.server.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	return s
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	if s.cfg.HasTLS() {
		slog.Info("HTTPS server listening", "addr", s.cfg.HTTP.ListenAddr)
		return s.server.ListenAndServeTLS(s.cfg.HTTP.TLSCertPath, s.cfg.HTTP.TLSKeyPath)
	}
	slog.Info("HTTP server listening (no TLS)", "addr", s.cfg.HTTP.ListenAddr)
	return s.server.ListenAndServe()
}

// Close shuts down the HTTP server.
func (s *Server) Close() error {
	return s.server.Close()
}

// requestLogger is a middleware that logs HTTP requests.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("HTTP request",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
		)
		next.ServeHTTP(w, r)
	})
}

// securityHeaders adds standard security headers to every response.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if s.cfg.HasTLS() {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// renderStatus writes an HTTP status code response.
func renderStatus(w http.ResponseWriter, code int) {
	http.Error(w, fmt.Sprintf("%d %s", code, http.StatusText(code)), code)
}
