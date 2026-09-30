//go:build e2e

package send_ban

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

// Failure cases: missing audits; invented backend operator identity; stale old
// values; no-op/CAS intent logged as a change; omitted legacy fields logged as
// mutations; unverified Manager actor; credentials or payloads in audit logs.
func TestSendBanManagementAudit(t *testing.T) {
	var events []map[string]any
	defer func() {
		path := os.Getenv("WK_E2E_SEND_BAN_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-send-ban-report.json")
		}
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "source_revision": os.Getenv("WK_E2E_SOURCE_REVISION"), "hash_slots": 256, "nodes": 1, "events": events}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path+".audit.json", append(raw, '\n'), 0644))
	}()
	secret := make([]byte, 32)
	_, err := rand.Read(secret)
	require.NoError(t, err)
	password := hex.EncodeToString(secret)
	users, err := json.Marshal([]map[string]any{{"username": "audit-operator", "password": password, "permissions": []map[string]any{{"resource": "*", "actions": []string{"*"}}}}})
	require.NoError(t, err)
	node := suite.New(t).StartSingleNodeCluster(suite.WithManagerHTTP(), suite.WithNodeConfigOverrides(1, map[string]string{
		"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_GATEWAY_TOKEN_AUTH_ON": "false",
		"WK_LOG_FORMAT": "json", "WK_MANAGER_AUTH_ON": "true", "WK_MANAGER_JWT_SECRET": password, "WK_MANAGER_USERS": string(users),
	}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	request := func(method, address, path, token string, body any, want int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(ctx, method, "http://"+address+path, bytes.NewReader(raw))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, want, resp.StatusCode)
		var out map[string]any
		require.NoError(t, json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out))
		return out
	}
	for i, body := range []map[string]any{
		{"uid": "audit-user", "send_ban": 1}, {"uid": "audit-user", "send_ban": 1},
		{"uid": "audit-user", "send_ban": 0, "expected_version": "0"}, {"uid": "audit-user", "send_ban": 0, "expected_version": "1"},
	} {
		status := http.StatusOK
		if i == 2 {
			status = http.StatusConflict
		}
		request("POST", node.APIAddr(), "/user/send_ban", "", body, status)
	}
	request("GET", node.APIAddr(), "/user/send_ban?uid=audit-user", "", nil, 200)
	request("POST", node.APIAddr(), "/channel", "", map[string]any{"channel_id": "audit-legacy", "channel_type": 2, "send_ban": 1}, 200)
	request("POST", node.APIAddr(), "/channel/info", "", map[string]any{"channel_id": "audit-legacy", "channel_type": 2}, 200)
	request("POST", node.APIAddr(), "/channel/info", "", map[string]any{"channel_id": "audit-legacy", "channel_type": 2, "send_ban": 0}, 200)
	login := request("POST", node.ManagerAddr(), "/manager/login", "", map[string]any{"username": "audit-operator", "password": password}, 200)
	token, ok := login["access_token"].(string)
	require.True(t, ok)
	request("POST", node.ManagerAddr(), "/manager/channels", token, map[string]any{"channel_id": "audit-manager", "channel_type": 2, "send_ban": false}, 201)
	request("PATCH", node.ManagerAddr(), "/manager/channels/2/audit-manager", token, map[string]any{"send_ban": true}, 200)

	file, err := os.Open(filepath.Join(node.Spec.LogDir, "app.log"))
	require.NoError(t, err)
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, 4<<20))
	for scanner.Scan() {
		var event map[string]any
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event["event"] != "internal.send_ban.mutation" {
			continue
		}
		require.False(t, strings.Contains(scanner.Text(), password) || strings.Contains(scanner.Text(), token), "audit must not expose credentials")
		events = append(events, event)
	}
	require.NoError(t, scanner.Err())
	require.Len(t, events, 8, "GET and omitted legacy policy must not emit mutation audits")
	for i, event := range events {
		require.Equal(t, true, event["policy_known"])
		if i < 6 {
			require.Equal(t, "unknown", event["operator"])
			require.Equal(t, "server_api", event["source"])
		} else {
			require.Equal(t, "audit-operator", event["operator"])
			require.Equal(t, "manager", event["source"])
		}
	}
	require.Equal(t, "version_conflict", events[2]["result"])
	require.Equal(t, float64(1), events[2]["previous_send_ban"])
	require.Equal(t, float64(1), events[2]["send_ban"])
	require.Equal(t, "1", events[2]["send_ban_version"])
	require.Equal(t, float64(1), events[3]["previous_send_ban"])
	require.Equal(t, float64(0), events[3]["send_ban"])
	require.Equal(t, "2", events[3]["send_ban_version"])
	for _, event := range events {
		for _, key := range []string{"token", "payload", "error"} {
			require.NotContains(t, event, key)
		}
	}
}
