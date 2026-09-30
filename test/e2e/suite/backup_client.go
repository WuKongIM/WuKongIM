//go:build e2e

package suite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BackupDashboard contains only public fields needed to observe archive/restore
// convergence. It does not decode repository credentials or internal metadata.
type BackupDashboard struct {
	State struct {
		Revision            uint64 `json:"revision"`
		ManagerSessionEpoch uint64 `json:"manager_session_epoch"`
		Plan                *struct {
			Revision uint64 `json:"revision"`
		} `json:"plan"`
		ActiveBackup  *struct{ ID, Status string }        `json:"active_backup"`
		ActiveRestore *PublicRestoreJob                   `json:"active_restore"`
		History       []struct{ ID, Kind, Status string } `json:"history"`
	} `json:"state"`
	Archives []struct{ ID, Health string } `json:"archives"`
}

// PublicRestoreJob identifies one public restore and its maintenance fence.
type PublicRestoreJob struct {
	ID                 string `json:"id"`
	Status             string `json:"status"`
	MaintenanceEntered bool   `json:"maintenance_entered"`
}

// BackupClient exercises authenticated public Manager HTTP. Only an explicit
// 401 refreshes login; mutations are never retried after an ambiguous outcome.
type BackupClient struct {
	addr, username, password, token string
}

func NewBackupClient(addr, username, password string) *BackupClient {
	return &BackupClient{addr: "http://" + addr, username: username, password: password}
}

func (c *BackupClient) login(ctx context.Context) error {
	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.request(ctx, http.MethodPost, "/manager/login", map[string]any{"username": c.username, "password": c.password}, &result); err != nil {
		return err
	}
	if result.AccessToken == "" {
		return fmt.Errorf("backup fixture: empty manager token")
	}
	c.token = result.AccessToken
	return nil
}

func (c *BackupClient) authenticated(ctx context.Context, method, path string, body, out any) error {
	if c.token == "" {
		if err := c.login(ctx); err != nil {
			return err
		}
	}
	err := c.request(ctx, method, path, body, out)
	if status, ok := err.(*HTTPStatusError); ok && status.StatusCode == http.StatusUnauthorized {
		if err = c.login(ctx); err != nil {
			return err
		}
		return c.request(ctx, method, path, body, out)
	}
	return err
}

func (c *BackupClient) Dashboard(ctx context.Context) (BackupDashboard, error) {
	var result BackupDashboard
	err := c.authenticated(ctx, http.MethodGet, "/manager/backups", nil, &result)
	return result, err
}

// EnableFilePlan tests the repository before enabling its immediate full backup.
// A definite revision conflict alone refreshes the saved plan revision. Peers
// must expose that revision before the repository probe resolves their mirrors.
func (c *BackupClient) EnableFilePlan(ctx context.Context, peers ...*BackupClient) error {
	plan := map[string]any{"enabled": false, "store": map[string]any{"kind": "file"}, "cron": "0 1 * * *", "time_zone": "Asia/Shanghai", "retention_count": 7, "rate_mib_per_second": 64, "workers_per_node": 4, "max_duration_hours": 12}
	var saved struct {
		Plan struct {
			Revision uint64 `json:"revision"`
		} `json:"plan"`
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		d, err := c.Dashboard(ctx)
		if err != nil {
			return err
		}
		var revision uint64
		if d.State.Plan != nil {
			revision = d.State.Plan.Revision
		}
		plan["expected_revision"] = revision
		err = c.authenticated(ctx, http.MethodPut, "/manager/backups/plan", plan, &saved)
		if err == nil {
			break
		}
		status, ok := err.(*HTTPStatusError)
		if !ok || status.StatusCode != http.StatusConflict || !strings.Contains(status.Body, `"error":"backup_plan_conflict"`) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	if saved.Plan.Revision == 0 {
		return fmt.Errorf("backup fixture: missing plan revision")
	}
	visibility, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, peer := range append([]*BackupClient{c}, peers...) {
		for {
			d, err := peer.Dashboard(visibility)
			if err != nil {
				return err
			}
			if d.State.Plan != nil && d.State.Plan.Revision == saved.Plan.Revision {
				break
			}
			select {
			case <-visibility.Done():
				return visibility.Err()
			case <-ticker.C:
			}
		}
	}
	if err := c.authenticated(ctx, http.MethodPost, "/manager/backups/repository/test", map[string]any{"expected_plan_revision": saved.Plan.Revision}, nil); err != nil {
		return err
	}
	plan["expected_revision"], plan["enabled"] = saved.Plan.Revision, true
	return c.authenticated(ctx, http.MethodPut, "/manager/backups/plan", plan, nil)
}

// Restore supplies the exact destructive-action confirmation through the public
// operator API. The caller chooses the archive; this helper never retries it.
func (c *BackupClient) Restore(ctx context.Context, archive string) (PublicRestoreJob, error) {
	var result PublicRestoreJob
	err := c.authenticated(ctx, http.MethodPost, "/manager/backups/archives/"+archive+"/restore", map[string]any{"username": c.username, "password": c.password, "confirmation": "RESTORE " + archive}, &result)
	return result, err
}

func (c *BackupClient) request(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.addr+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("backup fixture: response exceeds bound")
	}
	if resp.StatusCode/100 != 2 {
		return &HTTPStatusError{Method: method, URL: req.URL.String(), StatusCode: resp.StatusCode, Body: string(data)}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
