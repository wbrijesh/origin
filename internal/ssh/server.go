package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/gliderlabs/ssh"
	"github.com/jmoiron/sqlx"
	gossh "golang.org/x/crypto/ssh"

	"github.com/wbrijesh/origin/internal/config"
)

// Server is the SSH server for git operations.
type Server struct {
	cfg    *config.Config
	db     *sqlx.DB
	server *ssh.Server
}

// New creates a new SSH server.
func New(cfg *config.Config, db *sqlx.DB) (*Server, error) {
	s := &Server{
		cfg: cfg,
		db:  db,
	}

	hostKey, err := s.ensureHostKey()
	if err != nil {
		return nil, fmt.Errorf("host key: %w", err)
	}

	s.server = &ssh.Server{
		Addr:             cfg.SSH.ListenAddr,
		Handler:          s.handleSession,
		PublicKeyHandler: s.publicKeyHandler,
	}

	s.server.AddHostKey(hostKey)

	// Log the host key fingerprint
	pub := hostKey.PublicKey()
	fp := gossh.FingerprintSHA256(pub)
	slog.Info("SSH host key fingerprint", "fingerprint", fp)

	return s, nil
}

// ListenAndServe starts the SSH server.
func (s *Server) ListenAndServe() error {
	slog.Info("SSH server listening", "addr", s.cfg.SSH.ListenAddr)
	return s.server.ListenAndServe()
}

// Close shuts down the SSH server.
func (s *Server) Close() error {
	return s.server.Close()
}

// ensureHostKey loads or generates the SSH host key.
func (s *Server) ensureHostKey() (gossh.Signer, error) {
	keyPath := s.cfg.SSHHostKeyPath()

	// Try to load existing key
	if data, err := os.ReadFile(keyPath); err == nil {
		signer, err := gossh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("parse host key: %w", err)
		}
		slog.Info("loaded SSH host key", "path", keyPath)
		return signer, nil
	}

	// Generate new ed25519 key
	slog.Info("generating SSH host key", "path", keyPath)
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return nil, fmt.Errorf("create key directory: %w", err)
	}

	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	// Marshal to OpenSSH format
	pemBlock, err := gossh.MarshalPrivateKey(privKey, "")
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}

	keyData := pem.EncodeToMemory(pemBlock)
	if err := os.WriteFile(keyPath, keyData, 0o600); err != nil {
		return nil, fmt.Errorf("write host key: %w", err)
	}

	signer, err := gossh.NewSignerFromKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("create signer: %w", err)
	}

	return signer, nil
}

// publicKeyHandler verifies that the connecting user's public key
// is registered in the database.
func (s *Server) publicKeyHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	fp := gossh.FingerprintSHA256(key)

	var count int
	err := s.db.Get(&count, "SELECT COUNT(*) FROM ssh_keys WHERE fingerprint = ?", fp)
	if err != nil {
		slog.Error("SSH auth: database error", "error", err)
		return false
	}

	if count == 0 {
		slog.Warn("SSH auth: unknown key", "fingerprint", fp, "remote", ctx.RemoteAddr())
		return false
	}

	slog.Debug("SSH auth: accepted", "fingerprint", fp, "remote", ctx.RemoteAddr())
	return true
}
