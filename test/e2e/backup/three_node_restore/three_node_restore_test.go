//go:build e2e

package three_node_restore

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
	ID     string       `json:"id"`
	Status string       `json:"status"`
	Slots  []backupSlot `json:"slots"`
}

type backupSlot struct {
	Status string `json:"status"`
}

type restoreJob struct {
	ID     string `json:"id"`
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

type controllerRaftStatus struct {
	LeaderID uint64 `json:"leader_id"`
	Term     uint64 `json:"term"`
}

type channelMessagePage struct {
	Messages []struct {
		ClientMsgNo string `json:"client_msg_no"`
	} `json:"messages"`
}

func TestThreeNodeBackupSurvivesLeaderFailoverAndRestoresPointInTimeState(
	t *testing.T,
) {
	managerUsers, err := json.Marshal([]map[string]any{{
		"username": managerUsername,
		"password": managerPassword,
		"permissions": []map[string]any{
			{"resource": "cluster.backup", "actions": []string{"r", "w"}},
			{"resource": "cluster.restore", "actions": []string{"w"}},
			{"resource": "cluster.controller", "actions": []string{"r"}},
		},
	}})
	require.NoError(t, err)
	overrides := map[string]string{
		"WK_CLUSTER_HASH_SLOT_COUNT": "256",
		"WK_MANAGER_AUTH_ON":         "true",
		"WK_MANAGER_JWT_SECRET":      "three-node-backup-restore-e2e-secret",
		"WK_MANAGER_USERS":           string(managerUsers),
	}
	cluster := suite.New(t).StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithSharedBackupRepository(),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	managerNode := cluster.MustNode(1)
	token := managerLogin(t, ctx, *managerNode)
	const (
		channelID       = "three-node-backup-restore-group"
		onlineChannelID = "three-node-backup-online-group"
		firstClientMsg  = "three-node-backup-before"
		secondClientMsg = "three-node-backup-after"
	)
	for _, id := range []string{channelID, onlineChannelID} {
		require.NoError(t, suite.PostChannel(
			ctx, managerNode.APIAddr(), map[string]any{
				"channel_id": id, "channel_type": frame.ChannelTypeGroup,
				"subscribers": []string{"backup-sender", "backup-reader"},
			},
		), cluster.DumpDiagnostics())
	}
	sendBackupMessage(
		t, ctx, cluster, *managerNode, channelID,
		firstClientMsg, "before backup",
	)

	configureDailyFileBackup(t, ctx, *managerNode, token)
	waitForActiveBackupProgress(t, ctx, cluster, *managerNode, token)
	sendBackupMessage(
		t, ctx, cluster, *cluster.MustNode(2), onlineChannelID,
		"three-node-backup-online", "accepted while backup was active",
	)

	leader := waitForControllerLeader(
		t, ctx, cluster, *managerNode, token,
	)
	survivorID := uint64(1)
	if survivorID == leader.LeaderID {
		survivorID = 2
	}
	managerNode = cluster.MustNode(survivorID)
	require.NoError(
		t, cluster.MustNode(leader.LeaderID).Stop(), cluster.DumpDiagnostics(),
	)
	t.Logf(
		"stopped Controller Leader node %d at term %d during backup",
		leader.LeaderID, leader.Term,
	)

	dashboard := waitForHealthyArchive(
		t, ctx, cluster, *managerNode, &token,
	)
	require.NotEmpty(t, dashboard.Archives, cluster.DumpDiagnostics())
	archiveID := dashboard.Archives[0].ID
	t.Logf("backup resumed and published archive %s", archiveID)

	require.NoError(
		t, cluster.StartStoppedNode(leader.LeaderID), cluster.DumpDiagnostics(),
	)
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	sendBackupMessage(
		t, ctx, cluster, *managerNode, channelID,
		secondClientMsg, "after backup",
	)
	requireMessageClientNumbers(
		t, ctx, cluster, *managerNode, channelID,
		[]string{firstClientMsg, secondClientMsg},
	)

	admitted := admitRestoreEventually(
		t, ctx, cluster, *managerNode, &token, archiveID,
	)
	require.NotEmpty(t, admitted.ID)
	t.Logf("three-node restore admitted: %s", admitted.ID)

	waitForRestoreSuccess(t, ctx, cluster, *managerNode, &token)
	requireMessageClientNumbers(
		t, ctx, cluster, *managerNode, channelID,
		[]string{firstClientMsg},
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
			"rate_mib_per_second": 256,
			"workers_per_node":    4,
			"max_duration_hours":  12,
		}, nil)
}

func waitForActiveBackupProgress(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	node suite.StartedNode,
	token string,
) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last backupDashboard
	var lastErr error
	for {
		last = backupDashboard{}
		lastErr = managerJSONE(
			ctx, node, token, http.MethodGet, "/manager/backups", nil, &last,
		)
		if lastErr == nil && last.State.ActiveBackup != nil {
			completed := 0
			for _, slot := range last.State.ActiveBackup.Slots {
				if slot.Status == "complete" {
					completed++
				}
			}
			if completed > 0 && completed < len(last.State.ActiveBackup.Slots) {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"backup never became active: last=%#v err=%v\n%s",
				last, lastErr, cluster.DumpDiagnostics(),
			)
		case <-ticker.C:
		}
	}
}

func waitForControllerLeader(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	node suite.StartedNode,
	token string,
) controllerRaftStatus {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last controllerRaftStatus
	var lastErr error
	for {
		last = controllerRaftStatus{}
		lastErr = managerJSONE(
			ctx, node, token, http.MethodGet,
			"/manager/nodes/1/controller-raft", nil, &last,
		)
		if lastErr == nil && last.LeaderID != 0 && last.Term != 0 {
			return last
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"Controller Leader unavailable: last=%#v err=%v\n%s",
				last, lastErr, cluster.DumpDiagnostics(),
			)
		case <-ticker.C:
		}
	}
}

func waitForHealthyArchive(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	node suite.StartedNode,
	token *string,
) backupDashboard {
	t.Helper()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var last backupDashboard
	var lastErr error
	for {
		last = backupDashboard{}
		lastErr = managerJSONE(
			ctx, node, *token, http.MethodGet, "/manager/backups", nil, &last,
		)
		if statusCode(lastErr) == http.StatusUnauthorized {
			*token = managerLogin(t, ctx, node)
			continue
		}
		if lastErr == nil && last.State.ActiveBackup == nil &&
			len(last.Archives) > 0 &&
			last.Archives[0].Health == "healthy" {
			return last
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"backup did not publish: last=%#v err=%v\n%s",
				last, lastErr, cluster.DumpDiagnostics(),
			)
		case <-ticker.C:
		}
	}
}

func admitRestoreEventually(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	node suite.StartedNode,
	token *string,
	archiveID string,
) restoreJob {
	t.Helper()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var admitted restoreJob
	var lastErr error
	for {
		admitted = restoreJob{}
		lastErr = managerJSONE(
			ctx, node, *token, http.MethodPost,
			"/manager/backups/archives/"+archiveID+"/restore",
			map[string]any{
				"username": managerUsername, "password": managerPassword,
				"confirmation": "RESTORE " + archiveID,
			}, &admitted,
		)
		if statusCode(lastErr) == http.StatusUnauthorized {
			*token = managerLogin(t, ctx, node)
			continue
		}
		if lastErr == nil {
			return admitted
		}
		if statusCode(lastErr) != http.StatusConflict &&
			statusCode(lastErr) != http.StatusServiceUnavailable {
			t.Fatalf(
				"restore admission failed: err=%v\n%s",
				lastErr, cluster.DumpDiagnostics(),
			)
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"restore was not admitted: err=%v\n%s",
				lastErr, cluster.DumpDiagnostics(),
			)
		case <-ticker.C:
		}
	}
}

func waitForRestoreSuccess(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	node suite.StartedNode,
	token *string,
) {
	t.Helper()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var last backupDashboard
	var lastErr error
	for {
		last = backupDashboard{}
		lastErr = managerJSONE(
			ctx, node, *token, http.MethodGet, "/manager/backups", nil, &last,
		)
		if statusCode(lastErr) == http.StatusUnauthorized {
			*token = managerLogin(t, ctx, node)
			continue
		}
		if lastErr == nil && last.State.ActiveRestore == nil {
			for _, record := range last.State.History {
				if record.Kind == "restore" && record.Status == "succeeded" {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"restore did not succeed: last=%#v err=%v\n%s",
				last, lastErr, cluster.DumpDiagnostics(),
			)
		case <-ticker.C:
		}
	}
}

func sendBackupMessage(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	node suite.StartedNode,
	channelID string,
	clientMsgNo string,
	payload string,
) {
	t.Helper()
	result, err := suite.PostMessageSendEventually(
		ctx, node.APIAddr(), map[string]any{
			"from_uid": "backup-sender", "channel_id": channelID,
			"channel_type":  frame.ChannelTypeGroup,
			"client_msg_no": clientMsgNo,
			"payload":       base64.StdEncoding.EncodeToString([]byte(payload)),
		},
	)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(
		t, uint8(frame.ReasonSuccess), result.Reason, cluster.DumpDiagnostics(),
	)
}

func requireMessageClientNumbers(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
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
		last = channelMessagePage{}
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
			t.Fatalf(
				"message state mismatch: got=%#v want=%v err=%v\n%s",
				last, want, lastErr, cluster.DumpDiagnostics(),
			)
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
		map[string]any{
			"username": managerUsername, "password": managerPassword,
		}, &response)
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
