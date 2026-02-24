package hooks

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// VerifyPreReceive reads ref updates from stdin (the git pre-receive hook protocol),
// walks every new commit, and verifies that each one is signed with an SSH key
// listed in the allowed signers file built from the database.
//
// Environment variables expected:
//   - ORIGIN_DATA_PATH — path to the data directory
//   - ORIGIN_REPO_PATH — path to the bare repo
//   - ORIGIN_PUSHER_KEY_FINGERPRINT — fingerprint of the SSH key used to authenticate
func VerifyPreReceive(stdin io.Reader) error {
	dataPath := os.Getenv("ORIGIN_DATA_PATH")
	repoPath := os.Getenv("ORIGIN_REPO_PATH")
	pusherFP := os.Getenv("ORIGIN_PUSHER_KEY_FINGERPRINT")

	if dataPath == "" || repoPath == "" {
		return fmt.Errorf("missing required environment variables")
	}

	slog.Info("pre-receive: verifying commit signatures",
		"repo", os.Getenv("ORIGIN_REPO_NAME"),
		"pusher_fingerprint", pusherFP,
	)

	// Build allowed signers file from all SSH keys in the database
	allowedSignersPath, cleanup, err := buildAllowedSigners(dataPath)
	if err != nil {
		return fmt.Errorf("build allowed signers: %w", err)
	}
	defer cleanup()

	// Parse ref updates from stdin
	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) != 3 {
			continue
		}

		oldSHA := parts[0]
		newSHA := parts[1]
		// refName := parts[2]

		// Skip deletes
		if newSHA == strings.Repeat("0", 40) {
			continue
		}

		// Get list of new commits
		var revRange string
		if oldSHA == strings.Repeat("0", 40) {
			// New branch — verify all commits reachable from newSHA
			// that aren't reachable from any other ref
			revRange = newSHA + " --not --all"
		} else {
			revRange = oldSHA + ".." + newSHA
		}

		commits, err := listCommits(repoPath, revRange)
		if err != nil {
			return fmt.Errorf("list commits: %w", err)
		}

		for _, commitSHA := range commits {
			if err := verifyCommitSignature(repoPath, commitSHA, allowedSignersPath); err != nil {
				return fmt.Errorf("commit %s: %w", commitSHA[:7], err)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	slog.Info("pre-receive: all commits verified")
	return nil
}

// buildAllowedSigners creates a temporary allowed_signers file from all
// SSH public keys in the database. Returns the path, a cleanup function,
// and any error.
func buildAllowedSigners(dataPath string) (string, func(), error) {
	dbPath := filepath.Join(dataPath, "origin.db")

	// Query all public keys from the database using sqlite3 CLI
	// This avoids importing the full DB package in the hook context.
	// Format: "* <public_key>" (wildcard email, since we're single-user)
	cmd := exec.Command("sqlite3", dbPath, "SELECT public_key FROM ssh_keys;")
	output, err := cmd.Output()
	if err != nil {
		return "", nil, fmt.Errorf("query ssh keys: %w", err)
	}

	// Build allowed signers content
	var builder strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: <principal> <key-type> <key-data>
		// Using "*" as principal to match any email
		fmt.Fprintf(&builder, "* %s\n", line)
	}

	if builder.Len() == 0 {
		return "", nil, fmt.Errorf("no SSH keys found in database")
	}

	// Write to temp file
	tmpFile, err := os.CreateTemp("", "origin-allowed-signers-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}

	if _, err := tmpFile.WriteString(builder.String()); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("write allowed signers: %w", err)
	}
	tmpFile.Close()

	cleanup := func() {
		os.Remove(tmpFile.Name())
	}

	return tmpFile.Name(), cleanup, nil
}

// listCommits returns the SHA hashes of commits in the given rev range.
func listCommits(repoPath, revRange string) ([]string, error) {
	args := append([]string{"-C", repoPath, "rev-list"}, strings.Fields(revRange)...)
	cmd := exec.Command("git", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git rev-list %s: %w", revRange, err)
	}

	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			commits = append(commits, line)
		}
	}
	return commits, nil
}

// verifyCommitSignature verifies that a commit is signed with an SSH key
// present in the allowed signers file.
func verifyCommitSignature(repoPath, commitSHA, allowedSignersPath string) error {
	cmd := exec.Command("git",
		"-C", repoPath,
		"-c", "gpg.ssh.allowedSignersFile="+allowedSignersPath,
		"verify-commit", commitSHA,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("unsigned or invalid signature\n%s", string(output))
	}

	slog.Debug("pre-receive: verified commit", "sha", commitSHA[:7])
	return nil
}
