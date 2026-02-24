package ssh

import (
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gliderlabs/ssh"
	gossh "golang.org/x/crypto/ssh"

	gitpkg "github.com/wbrijesh/origin/internal/git"
)

// handleSession handles an incoming SSH session. It parses the git command
// and executes the appropriate git service (upload-pack or receive-pack).
func (s *Server) handleSession(sess ssh.Session) {
	cmd := sess.RawCommand()
	if cmd == "" {
		fmt.Fprintln(sess.Stderr(), "interactive SSH sessions are not supported")
		sess.Exit(1) //nolint:errcheck
		return
	}

	args := strings.Fields(cmd)
	if len(args) != 2 {
		fmt.Fprintf(sess.Stderr(), "invalid command: %s\n", cmd)
		sess.Exit(1) //nolint:errcheck
		return
	}

	serviceName := args[0]
	repoName := sanitizeRepoName(args[1])

	// Only allow git-upload-pack and git-receive-pack
	var service gitpkg.Service
	switch serviceName {
	case "git-upload-pack":
		service = gitpkg.UploadPackService
	case "git-receive-pack":
		service = gitpkg.ReceivePackService
	default:
		fmt.Fprintf(sess.Stderr(), "unsupported command: %s\n", serviceName)
		sess.Exit(1) //nolint:errcheck
		return
	}

	// Verify repo exists in database
	var repoID int
	err := s.db.Get(&repoID, "SELECT id FROM repositories WHERE name = ?", repoName)
	if err != nil {
		fmt.Fprintf(sess.Stderr(), "repository not found: %s\n", repoName)
		sess.Exit(1) //nolint:errcheck
		return
	}

	repoPath := filepath.Join(s.cfg.ReposPath(), repoName+".git")
	fp := gossh.FingerprintSHA256(sess.PublicKey())

	slog.Info("SSH git",
		"service", serviceName,
		"repo", repoName,
		"fingerprint", fp,
		"remote", sess.RemoteAddr(),
	)

	// Build environment for hooks
	env := []string{
		"ORIGIN_REPO_NAME=" + repoName,
		"ORIGIN_REPO_PATH=" + repoPath,
		"ORIGIN_PUSHER_KEY_FINGERPRINT=" + fp,
		"ORIGIN_DATA_PATH=" + s.cfg.DataPath,
	}

	// Execute git command
	gitCmd := exec.CommandContext(sess.Context(), "git", service.String()[4:], repoPath) // strip "git-" prefix
	gitCmd.Dir = repoPath
	gitCmd.Env = append(gitCmd.Environ(), env...)
	gitCmd.Stdin = sess
	gitCmd.Stdout = sess
	gitCmd.Stderr = sess.Stderr()

	if err := gitCmd.Run(); err != nil {
		slog.Error("SSH git command failed",
			"service", serviceName,
			"repo", repoName,
			"error", err,
		)
		sess.Exit(1) //nolint:errcheck
		return
	}

	sess.Exit(0) //nolint:errcheck
}

// sanitizeRepoName cleans up a repository name from git commands.
// Git clients send paths like '/my-repo.git' or 'my-repo.git'.
func sanitizeRepoName(name string) string {
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimSuffix(name, ".git")
	name = strings.Trim(name, "'\"")
	return filepath.Clean(name)
}
