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
	"time"

	"github.com/wbrijesh/origin/internal/webhook"
)

// RunPostReceive reads ref updates from stdin and triggers webhooks.
//
// Environment variables expected:
//   - ORIGIN_DATA_PATH — path to the data directory
//   - ORIGIN_REPO_NAME — repository name
//   - ORIGIN_REPO_PATH — path to the bare repo
//   - ORIGIN_PUSHER_KEY_FINGERPRINT — fingerprint of the pushing SSH key
func RunPostReceive(stdin io.Reader) error {
	dataPath := os.Getenv("ORIGIN_DATA_PATH")
	repoName := os.Getenv("ORIGIN_REPO_NAME")
	repoPath := os.Getenv("ORIGIN_REPO_PATH")
	pusherFP := os.Getenv("ORIGIN_PUSHER_KEY_FINGERPRINT")

	if dataPath == "" || repoName == "" {
		return fmt.Errorf("missing required environment variables")
	}

	// Update server info for dumb HTTP clients
	exec.Command("git", "-C", repoPath, "update-server-info").Run() //nolint:errcheck

	// Load webhooks from DB
	webhooks, err := loadWebhooks(dataPath, repoName)
	if err != nil {
		slog.Error("post-receive: load webhooks", "error", err)
		// Non-fatal — push still succeeds
		return nil
	}

	if len(webhooks) == 0 {
		return nil
	}

	// Parse ref updates and fire webhooks
	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) != 3 {
			continue
		}

		event := webhook.PushEvent{
			Event:     "push",
			Repo:      repoName,
			Ref:       parts[2],
			Before:    parts[0],
			After:     parts[1],
			Pusher:    pusherFP,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		webhook.Deliver(webhooks, event)
	}

	return nil
}

// loadWebhooks queries the database for active webhooks for a repo.
func loadWebhooks(dataPath, repoName string) ([]webhook.Webhook, error) {
	dbPath := filepath.Join(dataPath, "origin.db")
	query := fmt.Sprintf(
		"SELECT w.url, w.secret FROM webhooks w JOIN repositories r ON w.repo_id = r.id WHERE r.name = '%s' AND w.active = 1;",
		strings.ReplaceAll(repoName, "'", "''"),
	)

	cmd := exec.Command("sqlite3", "-separator", "|", dbPath, query)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("query webhooks: %w", err)
	}

	var webhooks []webhook.Webhook
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		wh := webhook.Webhook{URL: parts[0]}
		if len(parts) > 1 {
			wh.Secret = parts[1]
		}
		webhooks = append(webhooks, wh)
	}

	return webhooks, nil
}
