package hooks

import (
	"fmt"
	"os"
	"path/filepath"
)

// GenerateHooks writes the git hook scripts into a bare repository's hooks/ directory.
// The pre-receive hook calls back into the origin binary to verify commit signatures.
func GenerateHooks(repoPath, originBinaryPath string) error {
	hooksDir := filepath.Join(repoPath, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return fmt.Errorf("create hooks dir: %w", err)
	}

	// pre-receive hook: verify commit signatures
	preReceive := fmt.Sprintf(`#!/bin/sh
# Origin pre-receive hook — enforces SSH commit signing.
# This hook calls the origin binary to verify all pushed commits
# are signed with a registered SSH key.
exec "%s" hook pre-receive
`, originBinaryPath)

	preReceivePath := filepath.Join(hooksDir, "pre-receive")
	if err := os.WriteFile(preReceivePath, []byte(preReceive), 0o755); err != nil {
		return fmt.Errorf("write pre-receive hook: %w", err)
	}

	// post-receive hook: trigger webhooks and update server info
	postReceive := fmt.Sprintf(`#!/bin/sh
# Origin post-receive hook — triggers webhooks.
exec "%s" hook post-receive
`, originBinaryPath)

	postReceivePath := filepath.Join(hooksDir, "post-receive")
	if err := os.WriteFile(postReceivePath, []byte(postReceive), 0o755); err != nil {
		return fmt.Errorf("write post-receive hook: %w", err)
	}

	return nil
}
