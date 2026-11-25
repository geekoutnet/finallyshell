package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

var caddyVersionTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// LatestCaddyRelease requests GitHub's latest stable Caddy release on the
// local computer, never through SSH. A nil client uses the default transport;
// a child context caps the call at 12 seconds even with an injected client.
func LatestCaddyRelease(ctx context.Context, client *http.Client) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/caddyserver/caddy/releases/latest", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Caddy release lookup: HTTP %d", response.StatusCode)
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release); err != nil {
		return "", err
	}
	if !caddyVersionTag.MatchString(release.Tag) {
		return "", ErrCaddyUnsafe
	}
	return release.Tag, nil
}
