package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Mailpit reads the development inbox (http://127.0.0.1:8025).
type Mailpit struct {
	URL  string
	HTTP *http.Client
}

var tokenLink = regexp.MustCompile(`#token=([A-Za-z0-9_\-]+)`)

type mailSummary struct {
	ID      string    `json:"ID"`
	Created time.Time `json:"Created"`
}

// LinkToken waits for an email to address sent after since and returns the
// token of the account link it contains.
func (m Mailpit) LinkToken(ctx context.Context, address string, since time.Time) (string, error) {
	client := m.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	deadline := time.Now().Add(60 * time.Second)
	query := url.Values{"query": {fmt.Sprintf("to:%q", address)}, "limit": {"20"}}
	for {
		var list struct {
			Messages []mailSummary `json:"messages"`
		}
		if err := m.get(ctx, client, "/api/v1/search?"+query.Encode(), &list); err != nil {
			return "", err
		}
		for _, msg := range list.Messages { // newest first
			if msg.Created.Before(since.Add(-time.Second)) {
				break
			}
			var full struct {
				Text string `json:"Text"`
			}
			if err := m.get(ctx, client, "/api/v1/message/"+msg.ID, &full); err != nil {
				return "", err
			}
			if match := tokenLink.FindStringSubmatch(full.Text); match != nil {
				return match[1], nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("no account email for %s arrived within 60s", address)
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func (m Mailpit) get(ctx context.Context, client *http.Client, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(m.URL, "/")+path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("mailpit: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mailpit %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
