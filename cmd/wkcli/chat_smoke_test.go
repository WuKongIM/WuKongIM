package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Smoke test against a real WuKongIM server.
// Run with: go test -run TestSmoke -v -tags smoke
// Requires a running server (default http://127.0.0.1:5001).

func getTestServer() string {
	if s := os.Getenv("WKCLI_TEST_SERVER"); s != "" {
		return s
	}
	return "http://127.0.0.1:5001"
}

func smokeSetup(t *testing.T) (cleanup func()) {
	t.Helper()
	oldServer := flagServer
	oldToken := flagToken
	flagServer = getTestServer()
	flagToken = ""
	return func() {
		flagServer = oldServer
		flagToken = oldToken
	}
}

// ensureUser registers a user token via the HTTP API.
func ensureUser(t *testing.T, uid, token string) {
	t.Helper()
	body, code, _, err := doPost("/user/token", map[string]interface{}{
		"uid":          uid,
		"token":        token,
		"device_flag":  0,
		"device_level": 1,
	})
	require.NoError(t, err, "register user %s", uid)
	require.NoError(t, checkResponse(body, code), "register user %s response", uid)
}

// ensureChannel creates a channel with subscribers.
func ensureChannel(t *testing.T, channelID string, channelType int, subscribers []string) {
	t.Helper()
	body, code, _, err := doPost("/channel", map[string]interface{}{
		"channel_id":   channelID,
		"channel_type": channelType,
		"subscribers":  subscribers,
	})
	require.NoError(t, err, "create channel %s", channelID)
	require.NoError(t, checkResponse(body, code), "create channel %s response", channelID)
}

func TestSmokeChatConnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping smoke test in short mode")
	}

	cleanup := smokeSetup(t)
	defer cleanup()

	// Check server is reachable.
	body, code, _, err := doGet("/health")
	if err != nil {
		t.Skipf("server not reachable at %s: %v", flagServer, err)
	}
	if code != 200 {
		t.Skipf("server unhealthy: status %d, body: %s", code, string(body))
	}

	uid := fmt.Sprintf("smokeuser_%d", time.Now().UnixNano()%100000)
	token := uid
	channelID := fmt.Sprintf("smokechan_%d", time.Now().UnixNano()%100000)

	// Setup: register user and create channel.
	ensureUser(t, uid, token)
	ensureChannel(t, channelID, 2, []string{uid})

	// Step 1: Get WS route.
	t.Log("Step 1: Getting WS route...")
	wsAddr, err := getWSAddr(uid)
	require.NoError(t, err, "getWSAddr")
	require.NotEmpty(t, wsAddr)
	t.Logf("  ws_addr = %s", wsAddr)

	// Step 2: Connect via WebSocket.
	t.Log("Step 2: Connecting via WebSocket...")
	wsURL := wsAddr
	if !strings.HasPrefix(wsURL, "ws://") && !strings.HasPrefix(wsURL, "wss://") {
		wsURL = "ws://" + wsURL
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "WebSocket dial")
	defer conn.Close()

	session := &chatSession{
		conn:        conn,
		uid:         uid,
		token:       token,
		channelID:   channelID,
		channelType: 2,
		done:        make(chan struct{}),
	}

	// Step 3: Authenticate.
	t.Log("Step 3: Authenticating...")
	err = session.authenticate()
	require.NoError(t, err, "authenticate")
	t.Log("  Authenticated successfully")

	// Step 4: Send a message.
	t.Log("Step 4: Sending a message...")
	payload, _ := json.Marshal(map[string]string{
		"content": "smoke test message from wkcli",
	})
	sendReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "send",
		"id":      session.nextID(),
		"params": map[string]interface{}{
			"channelId":   channelID,
			"channelType": 2,
			"payload":     payload,
			"clientMsgNo": fmt.Sprintf("smoke_%d", time.Now().UnixNano()),
		},
	}
	err = session.writeJSON(sendReq)
	require.NoError(t, err, "send message")

	// Read the send response.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, respMsg, err := conn.ReadMessage()
	conn.SetReadDeadline(time.Time{})
	require.NoError(t, err, "read send response")

	var sendResp struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	err = json.Unmarshal(respMsg, &sendResp)
	require.NoError(t, err, "parse send response")
	assert.Nil(t, sendResp.Error, "send should not return error")
	t.Logf("  Message sent, response: %s", string(respMsg))

	// Step 5: Graceful disconnect.
	t.Log("Step 5: Disconnecting...")
	conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
	)
	t.Log("  Disconnected gracefully")

	t.Log("All smoke test steps passed")
}
