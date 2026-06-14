package top

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
)

type client struct {
	http *http.Client
}

func newClient() *client {
	return &client{http: &http.Client{Timeout: 5 * time.Second}}
}

func (c *client) snapshot(ctx context.Context, server string, cfg config) (accessapi.TopSnapshot, error) {
	endpoint, err := snapshotURL(server, cfg)
	if err != nil {
		return accessapi.TopSnapshot{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return accessapi.TopSnapshot{}, fmt.Errorf("build request for %s: %w", server, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return accessapi.TopSnapshot{}, fmt.Errorf("fetch top snapshot from %s: %w", server, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return accessapi.TopSnapshot{}, fmt.Errorf("fetch top snapshot from %s: status %d", server, resp.StatusCode)
	}
	var snapshot accessapi.TopSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return accessapi.TopSnapshot{}, fmt.Errorf("decode top snapshot from %s: %w", server, err)
	}
	return snapshot, nil
}

func snapshotURL(server string, cfg config) (string, error) {
	parsed, err := url.Parse(server)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("server %q must be an absolute http or https URL", server)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("server %q must use http or https", server)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/top/v1/snapshot"
	query := parsed.Query()
	query.Set("window", cfg.Window.String())
	query.Set("view", cfg.View)
	query.Set("limit", strconv.Itoa(cfg.Limit))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
