//go:build e2e

package scheduled_restore

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const (
	managerUsername = "backup-admin"
	managerPassword = "backup-admin-password"

	backupPhaseTimeout  = 6 * time.Minute
	restorePhaseTimeout = 8 * time.Minute
)

type managerLoginResponse struct {
	AccessToken string `json:"access_token"`
}

type backupDashboard struct {
	State struct {
		ActiveBackup  *backupJob  `json:"active_backup"`
		ActiveRestore *restoreJob `json:"active_restore"`
		History       []task      `json:"history"`
	} `json:"state"`
	Archives []archiveSummary `json:"archives"`
}

type backupJob struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type restoreJob struct {
	ID                string        `json:"id"`
	Status            string        `json:"status"`
	UpdatedUnixMillis int64         `json:"updated_unix_ms"`
	Slots             []restoreSlot `json:"slots"`
}

type restoreSlot struct {
	Status string `json:"status"`
}

type task struct {
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

type archiveSummary struct {
	ID     string `json:"id"`
	Health string `json:"health"`
}

type channelMessagePage struct {
	Messages []struct {
		ClientMsgNo string `json:"client_msg_no"`
		MessageSeq  uint64 `json:"message_seq"`
	} `json:"messages"`
}

func TestScheduledFullBackupRestoresPointInTimeBusinessState(t *testing.T) {
	managerUsers, err := json.Marshal([]map[string]any{{
		"username": managerUsername,
		"password": managerPassword,
		"permissions": []map[string]any{
			{"resource": "cluster.backup", "actions": []string{"r", "w"}},
			{"resource": "cluster.restore", "actions": []string{"w"}},
		},
	}})
	require.NoError(t, err)
	node := suite.New(t).StartSingleNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithNodeConfigOverrides(1, map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256",
			"WK_MANAGER_AUTH_ON":         "true",
			"WK_MANAGER_JWT_SECRET":      "scheduled-restore-e2e-jwt-secret",
			"WK_MANAGER_USERS":           string(managerUsers),
		}),
	)

	backupCtx, cancelBackup := context.WithTimeout(
		context.Background(), backupPhaseTimeout,
	)
	defer cancelBackup()
	token := managerLogin(t, backupCtx, *node)

	const (
		channelID       = "backup-restore-e2e-group"
		firstClientMsg  = "backup-restore-before"
		secondClientMsg = "backup-restore-after"
	)
	require.NoError(t, suite.PostChannel(backupCtx, node.APIAddr(), map[string]any{
		"channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
		"subscribers": []string{"backup-sender", "backup-reader"},
	}), node.DumpDiagnostics())
	sendBackupMessage(t, backupCtx, *node, channelID, firstClientMsg, "before backup")

	configureDailyFileBackup(t, backupCtx, *node, token)
	dashboard := waitForHealthyArchive(t, backupCtx, *node, &token)
	require.Len(t, dashboard.Archives, 1, node.DumpDiagnostics())
	archiveID := dashboard.Archives[0].ID
	t.Logf("healthy archive published: %s", archiveID)

	sendBackupMessage(t, backupCtx, *node, channelID, secondClientMsg, "after backup")
	t.Log("post-backup message accepted")
	requireMessageClientNumbers(
		t, backupCtx, *node, channelID, []string{firstClientMsg, secondClientMsg},
	)
	t.Log("pre-restore business state verified")
	cancelBackup()

	restoreCtx, cancelRestore := context.WithTimeout(
		context.Background(), restorePhaseTimeout,
	)
	defer cancelRestore()
	var admitted restoreJob
	managerJSON(t, restoreCtx, *node, token, http.MethodPost,
		"/manager/backups/archives/"+archiveID+"/restore",
		map[string]any{
			"username": managerUsername, "password": managerPassword,
			"confirmation": "RESTORE " + archiveID,
		}, &admitted)
	require.NotEmpty(t, admitted.ID)
	t.Logf("restore admitted: %s", admitted.ID)

	waitForRestoreSuccess(t, restoreCtx, *node, &token)
	t.Log("restore completed")
	requireMessageClientNumbers(
		t, restoreCtx, *node, channelID, []string{firstClientMsg},
	)
}

func configureDailyFileBackup(
	t *testing.T,
	ctx context.Context,
	node suite.StartedNode,
	token string,
) {
	t.Helper()
	managerJSON(t, ctx, node, token, http.MethodPut, "/manager/backups/plan",
		map[string]any{
			"expected_revision":   0,
			"enabled":             true,
			"store":               map[string]any{"kind": "file"},
			"cron":                "0 1 * * *",
			"time_zone":           "Asia/Shanghai",
			"retention_count":     7,
			"rate_mib_per_second": 64,
			"workers_per_node":    4,
			"max_duration_hours":  12,
		}, nil)
}

func sendBackupMessage(
	t *testing.T,
	ctx context.Context,
	node suite.StartedNode,
	channelID string,
	clientMsgNo string,
	payload string,
) {
	t.Helper()
	result, err := suite.PostMessageSendEventually(ctx, node.APIAddr(), map[string]any{
		"from_uid": "backup-sender", "channel_id": channelID,
		"channel_type":  frame.ChannelTypeGroup,
		"client_msg_no": clientMsgNo,
		"payload":       base64.StdEncoding.EncodeToString([]byte(payload)),
	})
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), result.Reason, node.DumpDiagnostics())
}

func waitForHealthyArchive(
	t *testing.T,
	ctx context.Context,
	node suite.StartedNode,
	token *string,
) backupDashboard {
	t.Helper()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var last backupDashboard
	for {
		last = backupDashboard{}
		err := managerJSONE(
			ctx, node, *token, http.MethodGet, "/manager/backups", nil, &last,
		)
		if statusCode(err) == http.StatusUnauthorized {
			*token = managerLogin(t, ctx, node)
			continue
		}
		if err == nil && last.State.ActiveBackup == nil &&
			len(last.Archives) > 0 &&
			last.Archives[0].Health == "healthy" {
			return last
		}
		select {
		case <-ctx.Done():
			t.Fatalf("backup did not publish: last=%#v err=%v\n%s", last, err, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func waitForRestoreSuccess(
	t *testing.T,
	ctx context.Context,
	node suite.StartedNode,
	token *string,
) {
	t.Helper()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var last backupDashboard
	for {
		last = backupDashboard{}
		err := managerJSONE(
			ctx, node, *token, http.MethodGet, "/manager/backups", nil, &last,
		)
		if statusCode(err) == http.StatusUnauthorized {
			*token = managerLogin(t, ctx, node)
			continue
		}
		if err == nil && last.State.ActiveRestore == nil {
			for _, record := range last.State.History {
				if record.Kind == "restore" && record.Status == "succeeded" {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"restore did not succeed: progress=%s history=%#v archives=%#v err=%v\n%s",
				describeRestoreProgress(last.State.ActiveRestore),
				last.State.History, last.Archives, err, node.DumpDiagnostics(),
			)
		case <-ticker.C:
		}
	}
}

func describeRestoreProgress(job *restoreJob) string {
	if job == nil {
		return "none"
	}
	counts := map[string]int{}
	for _, slot := range job.Slots {
		counts[slot.Status]++
	}
	return fmt.Sprintf(
		"id=%s status=%s updated_unix_ms=%d slots=%v",
		job.ID, job.Status, job.UpdatedUnixMillis, counts,
	)
}

func requireMessageClientNumbers(
	t *testing.T,
	ctx context.Context,
	node suite.StartedNode,
	channelID string,
	want []string,
) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last channelMessagePage
	var lastErr error
	for {
		_, lastErr = suite.PostJSON(
			ctx, "http://"+node.APIAddr()+"/channel/messagesync",
			map[string]any{
				"login_uid": "backup-reader", "channel_id": channelID,
				"channel_type":      frame.ChannelTypeGroup,
				"start_message_seq": 0, "limit": 10,
			}, &last,
		)
		if lastErr == nil && len(last.Messages) == len(want) {
			matched := true
			for index := range want {
				if last.Messages[index].ClientMsgNo != want[index] {
					matched = false
					break
				}
			}
			if matched {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("message state mismatch: got=%#v want=%v err=%v\n%s", last, want, lastErr, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func managerLogin(
	t *testing.T,
	ctx context.Context,
	node suite.StartedNode,
) string {
	t.Helper()
	var response managerLoginResponse
	managerJSON(t, ctx, node, "", http.MethodPost, "/manager/login",
		map[string]any{"username": managerUsername, "password": managerPassword},
		&response)
	require.NotEmpty(t, response.AccessToken)
	return response.AccessToken
}

func managerJSON(
	t *testing.T,
	ctx context.Context,
	node suite.StartedNode,
	token string,
	method string,
	path string,
	body any,
	out any,
) {
	t.Helper()
	require.NoError(
		t, managerJSONE(ctx, node, token, method, path, body, out),
		node.DumpDiagnostics(),
	)
}

func managerJSONE(
	ctx context.Context,
	node suite.StartedNode,
	token string,
	method string,
	path string,
	body any,
	out any,
) error {
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		requestBody = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(
		ctx, method, "http://"+node.ManagerAddr()+path, requestBody,
	)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return &suite.HTTPStatusError{
			Method: method, URL: req.URL.String(),
			StatusCode: resp.StatusCode, Body: string(responseBody),
		}
	}
	if out != nil && len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, out); err != nil {
			return fmt.Errorf("decode Manager response: %w", err)
		}
	}
	return nil
}

func statusCode(err error) int {
	if status, ok := err.(*suite.HTTPStatusError); ok {
		return status.StatusCode
	}
	return 0
}
