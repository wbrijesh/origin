package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// PushEvent is the JSON payload delivered to webhook URLs on push.
type PushEvent struct {
	Event     string `json:"event"`
	Repo      string `json:"repository"`
	Ref       string `json:"ref"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Pusher    string `json:"pusher"`
	Timestamp string `json:"timestamp"`
}

// Webhook represents a webhook configuration.
type Webhook struct {
	URL    string
	Secret string
}

// Deliver sends a push event to all provided webhooks.
// Delivery is fire-and-forget with a 5-second timeout.
func Deliver(webhooks []Webhook, event PushEvent) {
	payload, err := json.Marshal(event)
	if err != nil {
		slog.Error("webhook: marshal payload", "error", err)
		return
	}

	for _, wh := range webhooks {
		go deliver(wh, payload)
	}
}

func deliver(wh Webhook, payload []byte) {
	client := &http.Client{Timeout: 5 * time.Second}

	req, err := http.NewRequest(http.MethodPost, wh.URL, bytes.NewReader(payload))
	if err != nil {
		slog.Error("webhook: create request", "url", wh.URL, "error", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Origin-Webhook/1.0")
	req.Header.Set("X-Origin-Event", "push")

	// HMAC signature if secret is configured
	if wh.Secret != "" {
		mac := hmac.New(sha256.New, []byte(wh.Secret))
		mac.Write(payload)
		sig := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Origin-Signature", fmt.Sprintf("sha256=%s", sig))
	}

	resp, err := client.Do(req)
	if err != nil {
		slog.Error("webhook: delivery failed", "url", wh.URL, "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		slog.Warn("webhook: non-success response", "url", wh.URL, "status", resp.StatusCode)
	} else {
		slog.Info("webhook: delivered", "url", wh.URL, "status", resp.StatusCode)
	}
}
