package http

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	gitpkg "github.com/wbrijesh/origin/internal/git"
	"github.com/wbrijesh/origin/internal/hooks"
)

// --- Initial Setup ---

// needsSetup returns true if no admin password has been configured yet.
func (s *Server) needsSetup() bool {
	var count int
	err := s.db.Get(&count, "SELECT COUNT(*) FROM settings WHERE key = 'password_hash'")
	return err != nil || count == 0
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !s.needsSetup() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	data := s.baseData(r)
	data["Title"] = "Setup"
	s.render.render(w, "setup", data)
}

func (s *Server) handleSetupPost(w http.ResponseWriter, r *http.Request) {
	if !s.needsSetup() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	if username == "" {
		username = "admin"
	}

	data := s.baseData(r)
	data["Title"] = "Setup"

	if len(password) < 8 {
		data["Error"] = "Password must be at least 8 characters."
		s.render.render(w, "setup", data)
		return
	}

	if password != confirm {
		data["Error"] = "Passwords do not match."
		s.render.render(w, "setup", data)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Failed to hash password")
		return
	}

	s.db.Exec("INSERT INTO settings (key, value) VALUES ('password_hash', ?)", string(hash))           //nolint:errcheck
	s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES ('admin_username', ?)", username) //nolint:errcheck

	slog.Info("admin account created", "username", username)

	// Auto-login after setup
	s.createSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- Authentication ---

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// Redirect to setup if no password is configured
	if s.needsSetup() {
		http.Redirect(w, r, "/-/setup", http.StatusSeeOther)
		return
	}
	data := s.baseData(r)
	data["Title"] = "Login"
	s.render.render(w, "login", data)
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if s.needsSetup() {
		http.Redirect(w, r, "/-/setup", http.StatusSeeOther)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	// Check credentials
	var storedHash string
	if err := s.db.Get(&storedHash, "SELECT value FROM settings WHERE key = 'password_hash'"); err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Password not configured")
		return
	}

	// Get stored username
	var storedUsername string
	err := s.db.Get(&storedUsername, "SELECT value FROM settings WHERE key = 'admin_username'")
	if err != nil {
		storedUsername = "admin"
	}

	if username != storedUsername || bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password)) != nil {
		data := s.baseData(r)
		data["Title"] = "Login"
		data["Error"] = "Invalid username or password."
		s.render.render(w, "login", data)
		return
	}

	// Create session
	s.createSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err == nil {
		s.db.Exec("DELETE FROM sessions WHERE id = ?", cookie.Value) //nolint:errcheck
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// requireAuth is a middleware that redirects to login if not authenticated.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isLoggedIn(r) {
			http.Redirect(w, r, "/-/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// --- SSH Key Management ---

func (s *Server) handleAddSSHKey(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	publicKey := strings.TrimSpace(r.FormValue("public_key"))

	if name == "" || publicKey == "" {
		http.Redirect(w, r, "/-/settings", http.StatusSeeOther)
		return
	}

	// Compute fingerprint using ssh-keygen
	fp, err := computeFingerprint(publicKey)
	if err != nil {
		slog.Error("invalid SSH key", "error", err)
		http.Redirect(w, r, "/-/settings", http.StatusSeeOther)
		return
	}

	_, err = s.db.Exec(
		"INSERT INTO ssh_keys (name, public_key, fingerprint) VALUES (?, ?, ?)",
		name, publicKey, fp,
	)
	if err != nil {
		slog.Error("insert SSH key", "error", err)
	}

	http.Redirect(w, r, "/-/settings", http.StatusSeeOther)
}

func (s *Server) handleDeleteSSHKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.db.Exec("DELETE FROM ssh_keys WHERE id = ?", id) //nolint:errcheck
	http.Redirect(w, r, "/-/settings", http.StatusSeeOther)
}

// --- Access Token Management ---

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, "/-/settings", http.StatusSeeOther)
		return
	}

	rawToken := "origin_" + generateToken()
	hash := sha256Hash(rawToken)

	_, err := s.db.Exec(
		"INSERT INTO access_tokens (name, token_hash) VALUES (?, ?)",
		name, hash,
	)
	if err != nil {
		slog.Error("create token", "error", err)
		http.Redirect(w, r, "/-/settings", http.StatusSeeOther)
		return
	}

	// Show the settings page with the new token visible
	s.handleSettingsWithNewToken(w, r, rawToken)
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.db.Exec("DELETE FROM access_tokens WHERE id = ?", id) //nolint:errcheck
	http.Redirect(w, r, "/-/settings", http.StatusSeeOther)
}

// --- Password Change ---

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")

	var storedHash string
	if err := s.db.Get(&storedHash, "SELECT value FROM settings WHERE key = 'password_hash'"); err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Password not configured")
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(currentPassword)) != nil {
		http.Redirect(w, r, "/-/settings", http.StatusSeeOther)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Failed to hash password")
		return
	}

	s.db.Exec("UPDATE settings SET value = ? WHERE key = 'password_hash'", string(hash)) //nolint:errcheck
	http.Redirect(w, r, "/-/settings", http.StatusSeeOther)
}

// --- Repository Management ---

func (s *Server) handleNewRepo(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r)
	data["Title"] = "New Repository"
	s.render.render(w, "new_repo", data)
}

func (s *Server) handleCreateRepo(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	description := strings.TrimSpace(r.FormValue("description"))
	isPrivate := r.FormValue("is_private") == "on"

	if name == "" {
		data := s.baseData(r)
		data["Title"] = "New Repository"
		data["Error"] = "Repository name is required."
		s.render.render(w, "new_repo", data)
		return
	}

	// Validate name
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.') {
			data := s.baseData(r)
			data["Title"] = "New Repository"
			data["Error"] = "Invalid name. Use letters, numbers, hyphens, dots, and underscores only."
			s.render.render(w, "new_repo", data)
			return
		}
	}

	// Create bare git repo
	repoPath := filepath.Join(s.cfg.ReposPath(), name+".git")
	cmd := exec.Command("git", "init", "--bare", repoPath)
	if err := cmd.Run(); err != nil {
		slog.Error("git init failed", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "Failed to create repository")
		return
	}

	// Generate hooks
	originBin, _ := os.Executable()
	if err := hooks.GenerateHooks(repoPath, originBin); err != nil {
		slog.Error("generate hooks failed", "error", err)
	}

	// Insert into database
	privateInt := 0
	if isPrivate {
		privateInt = 1
	}
	_, err := s.db.Exec(
		"INSERT INTO repositories (name, description, is_private) VALUES (?, ?, ?)",
		name, description, privateInt,
	)
	if err != nil {
		slog.Error("insert repo", "error", err)
		// Clean up filesystem
		os.RemoveAll(repoPath)
		data := s.baseData(r)
		data["Title"] = "New Repository"
		data["Error"] = "Repository name already taken."
		s.render.render(w, "new_repo", data)
		return
	}

	http.Redirect(w, r, "/"+name+"/", http.StatusSeeOther)
}

func (s *Server) handleRepoSettings(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))

	data := s.baseData(r)
	data["Title"] = fmt.Sprintf("%s — settings", repoName)
	data["RepoName"] = repoName

	var repo repoRow
	if err := s.db.Get(&repo, "SELECT name, description, is_private, updated_at FROM repositories WHERE name = ?", repoName); err != nil {
		s.renderError(w, r, http.StatusNotFound, "Repository not found")
		return
	}

	data["Description"] = repo.Description
	data["IsPrivate"] = repo.IsPrivate

	gitRepo, err := gitpkg.OpenRepo(s.cfg.ReposPath(), repoName)
	if err != nil {
		data["DefaultBranch"] = "main"
	} else {
		data["DefaultBranch"] = gitpkg.DefaultBranch(gitRepo)
	}

	// Load webhooks
	type webhookRow struct {
		ID     int    `db:"id"`
		URL    string `db:"url"`
		Active bool   `db:"active"`
	}
	var webhooks []webhookRow
	s.db.Select(&webhooks, "SELECT id, url, active FROM webhooks WHERE repo_id = (SELECT id FROM repositories WHERE name = ?)", repoName) //nolint:errcheck
	data["Webhooks"] = webhooks

	s.render.render(w, "repo_settings", data)
}

func (s *Server) handleUpdateRepoSettings(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))
	description := strings.TrimSpace(r.FormValue("description"))
	isPrivate := r.FormValue("is_private") == "on"
	defaultBranch := strings.TrimSpace(r.FormValue("default_branch"))

	privateInt := 0
	if isPrivate {
		privateInt = 1
	}

	s.db.Exec(
		"UPDATE repositories SET description = ?, is_private = ?, updated_at = CURRENT_TIMESTAMP WHERE name = ?",
		description, privateInt, repoName,
	) //nolint:errcheck

	// Update HEAD if default branch changed
	if defaultBranch != "" {
		repoPath := filepath.Join(s.cfg.ReposPath(), repoName+".git")
		exec.Command("git", "-C", repoPath, "symbolic-ref", "HEAD", "refs/heads/"+defaultBranch).Run() //nolint:errcheck
	}

	http.Redirect(w, r, "/"+repoName+"/-/settings", http.StatusSeeOther)
}

func (s *Server) handleRenameRepo(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))
	newName := strings.TrimSpace(r.FormValue("new_name"))

	if newName == "" || newName == repoName {
		http.Redirect(w, r, "/"+repoName+"/-/settings", http.StatusSeeOther)
		return
	}

	oldPath := filepath.Join(s.cfg.ReposPath(), repoName+".git")
	newPath := filepath.Join(s.cfg.ReposPath(), newName+".git")

	if err := os.Rename(oldPath, newPath); err != nil {
		slog.Error("rename repo", "error", err)
		http.Redirect(w, r, "/"+repoName+"/-/settings", http.StatusSeeOther)
		return
	}

	s.db.Exec("UPDATE repositories SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE name = ?", newName, repoName) //nolint:errcheck

	http.Redirect(w, r, "/"+newName+"/-/settings", http.StatusSeeOther)
}

func (s *Server) handleDeleteRepo(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))

	repoPath := filepath.Join(s.cfg.ReposPath(), repoName+".git")
	os.RemoveAll(repoPath)

	s.db.Exec("DELETE FROM repositories WHERE name = ?", repoName) //nolint:errcheck

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- Webhook Management ---

func (s *Server) handleAddWebhook(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))
	url := strings.TrimSpace(r.FormValue("url"))
	secret := strings.TrimSpace(r.FormValue("secret"))

	if url == "" {
		http.Redirect(w, r, "/"+repoName+"/-/settings", http.StatusSeeOther)
		return
	}

	var repoID int
	if err := s.db.Get(&repoID, "SELECT id FROM repositories WHERE name = ?", repoName); err != nil {
		http.Redirect(w, r, "/"+repoName+"/-/settings", http.StatusSeeOther)
		return
	}

	s.db.Exec("INSERT INTO webhooks (repo_id, url, secret) VALUES (?, ?, ?)", repoID, url, secret) //nolint:errcheck
	http.Redirect(w, r, "/"+repoName+"/-/settings", http.StatusSeeOther)
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))
	id := r.PathValue("wid")
	s.db.Exec("DELETE FROM webhooks WHERE id = ?", id) //nolint:errcheck
	http.Redirect(w, r, "/"+repoName+"/-/settings", http.StatusSeeOther)
}

// --- Settings Page ---

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.handleSettingsWithNewToken(w, r, "")
}

func (s *Server) handleSettingsWithNewToken(w http.ResponseWriter, r *http.Request, newToken string) {
	data := s.baseData(r)
	data["Title"] = "Settings"

	type sshKeyRow struct {
		ID          int       `db:"id"`
		Name        string    `db:"name"`
		Fingerprint string    `db:"fingerprint"`
		CreatedAt   time.Time `db:"created_at"`
	}
	var keys []sshKeyRow
	s.db.Select(&keys, "SELECT id, name, fingerprint, created_at FROM ssh_keys ORDER BY created_at DESC") //nolint:errcheck
	data["SSHKeys"] = keys

	type tokenRow struct {
		ID        int        `db:"id"`
		Name      string     `db:"name"`
		ExpiresAt *time.Time `db:"expires_at"`
		CreatedAt time.Time  `db:"created_at"`
	}
	var tokens []tokenRow
	s.db.Select(&tokens, "SELECT id, name, expires_at, created_at FROM access_tokens ORDER BY created_at DESC") //nolint:errcheck
	data["Tokens"] = tokens

	if newToken != "" {
		data["NewToken"] = newToken
	}

	s.render.render(w, "settings", data)
}

// --- Session Helpers ---

// createSession creates a new session and sets the cookie.
func (s *Server) createSession(w http.ResponseWriter) {
	sessionID := generateToken()
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	s.db.Exec("INSERT INTO sessions (id, expires_at) VALUES (?, ?)", sessionID, expiresAt) //nolint:errcheck

	// Clean up expired sessions occasionally
	s.db.Exec("DELETE FROM sessions WHERE expires_at < CURRENT_TIMESTAMP") //nolint:errcheck

	cookie := &http.Cookie{
		Name:     "session",
		Value:    sessionID,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}

	// Set Secure flag when using TLS
	if s.cfg.HasTLS() {
		cookie.Secure = true
	}

	http.SetCookie(w, cookie)
}

// --- Helpers ---

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b)
}

func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func computeFingerprint(publicKey string) (string, error) {
	// Write key to temp file and use ssh-keygen to get fingerprint
	tmpFile, err := os.CreateTemp("", "origin-key-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(publicKey) //nolint:errcheck
	tmpFile.Close()

	cmd := exec.Command("ssh-keygen", "-lf", tmpFile.Name())
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("invalid SSH key: %w", err)
	}

	// Output format: "256 SHA256:abc123... comment (ED25519)"
	parts := strings.Fields(string(output))
	if len(parts) < 2 {
		return "", fmt.Errorf("unexpected ssh-keygen output")
	}

	return parts[1], nil
}
