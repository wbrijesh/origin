package http

import (
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	gitpkg "github.com/wbrijesh/origin/internal/git"
)

// gitInfoRefs handles GET /{repo}/info/refs?service=git-upload-pack
// This is the smart HTTP ref advertisement endpoint (read-only).
func (s *Server) gitInfoRefs(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))
	service := r.URL.Query().Get("service")

	if service != "git-upload-pack" {
		// We only support upload-pack (read-only). Deny receive-pack.
		if service == "git-receive-pack" {
			http.Error(w, "push over HTTP is not supported — use SSH", http.StatusForbidden)
			return
		}
		renderStatus(w, http.StatusBadRequest)
		return
	}

	// Check if repo exists and is accessible
	if !s.canReadRepo(repoName) {
		renderStatus(w, http.StatusNotFound)
		return
	}

	repoPath := filepath.Join(s.cfg.ReposPath(), repoName+".git")

	w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	// Write pktline service header
	gitpkg.WritePktline(w, "# service=git-upload-pack") //nolint:errcheck

	// Run git upload-pack --stateless-rpc --advertise-refs
	cmd := gitpkg.ServiceCommand{
		Dir:    repoPath,
		Args:   []string{"--stateless-rpc", "--advertise-refs"},
		Stdout: w,
	}

	if err := gitpkg.UploadPackService.Run(r.Context(), cmd); err != nil {
		slog.Error("git info/refs failed", "repo", repoName, "error", err)
		return
	}
}

// gitUploadPack handles POST /{repo}/git-upload-pack
// This is the smart HTTP data exchange endpoint (read-only).
func (s *Server) gitUploadPack(w http.ResponseWriter, r *http.Request) {
	repoName := sanitizeRepoPath(r.PathValue("repo"))

	if !s.canReadRepo(repoName) {
		renderStatus(w, http.StatusNotFound)
		return
	}

	repoPath := filepath.Join(s.cfg.ReposPath(), repoName+".git")

	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "Keep-Alive")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	// Handle gzip-encoded request bodies
	var reader io.ReadCloser = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			slog.Error("gzip reader failed", "error", err)
			return
		}
		defer gz.Close()
		reader = gz
	}

	cmd := gitpkg.ServiceCommand{
		Dir:    repoPath,
		Args:   []string{"--stateless-rpc"},
		Stdin:  reader,
		Stdout: w,
	}

	if err := gitpkg.UploadPackService.Run(r.Context(), cmd); err != nil {
		slog.Error("git upload-pack failed", "repo", repoName, "error", err)
		return
	}
}

// gitReceivePackDenied handles POST /{repo}/git-receive-pack with a 403.
// Push is only allowed over SSH.
func (s *Server) gitReceivePackDenied(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "push over HTTP is not supported — use SSH", http.StatusForbidden)
}

// canReadRepo checks if a repository exists and is readable.
// For now, it checks that the repo is in the DB. Access control for
// private repos will be added in Phase 7.
func (s *Server) canReadRepo(name string) bool {
	var isPrivate bool
	err := s.db.Get(&isPrivate, "SELECT is_private FROM repositories WHERE name = ?", name)
	if err != nil {
		return false // repo doesn't exist
	}
	// TODO: Phase 7 — check authentication for private repos
	if isPrivate {
		return false // for now, private repos are not accessible via HTTP
	}
	return true
}

// sanitizeRepoPath cleans a repo name from the URL path.
func sanitizeRepoPath(name string) string {
	name = strings.TrimSuffix(name, ".git")
	name = strings.Trim(name, "/")
	return filepath.Clean(name)
}

// formatSize formats a byte count into a human-readable string.
func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
