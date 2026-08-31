//go:build e2e

package easy_sdk_jsonrpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

const (
	personChannelType = 1
	reasonSuccess     = 1
	clientIOTimeout   = 8 * time.Second
)

type fieldDialect uint8

const (
	camelCase fieldDialect = iota
	snakeCase
)

type easySDKProfile struct {
	name                string
	dialect             fieldDialect
	outboundMessageType int
	requireInboundText  bool
}

var (
	iosProfile = easySDKProfile{
		name:                "EasySDK iOS v1.1.0",
		dialect:             camelCase,
		outboundMessageType: websocket.BinaryMessage,
	}
	androidProfile = easySDKProfile{
		name:                "EasySDK Android v1.0.4",
		dialect:             snakeCase,
		outboundMessageType: websocket.TextMessage,
		requireInboundText:  true,
	}
)

func TestEasySDKJSONRPCAndroidAndIOSExchangeMessagesAndReconnect(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster(
		suite.WithWebSocketGateway(),
		suite.WithNodeConfigOverrides(1, map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256",
		}),
	)
	httpClient := &http.Client{Timeout: time.Second}

	alice := dialEasySDKClient(t, node.WebSocketURL(), iosProfile)
	bob := dialEasySDKClient(t, node.WebSocketURL(), androidProfile)

	requireConnect(t, alice, connectParams(iosProfile, "easy-ios-alice", "ios-alice-device", "alice-token"), node.DumpDiagnostics())
	requireConnect(t, bob, connectParams(androidProfile, "easy-android-bob", "android-bob-device", "bob-token"), node.DumpDiagnostics())
	requireOnlineUsers(t, httpClient, node.APIAddr(), []string{"easy-ios-alice", "easy-android-bob"}, map[string]bool{
		"easy-ios-alice":   true,
		"easy-android-bob": true,
	}, node.DumpDiagnostics)

	iosPayload := map[string]any{"type": 1, "content": "hello from iOS binary"}
	iosClientMsgNo := "ios-to-android-" + uuid.NewString()
	iosAck := requireSend(t, alice, sendParams(t, iosProfile, iosClientMsgNo, "easy-android-bob", iosPayload), node.DumpDiagnostics())
	androidRecv := requireRecv(t, bob, "easy-ios-alice", iosClientMsgNo, iosAck, iosPayload, node.DumpDiagnostics())
	require.NoError(t, bob.notify("recvack", recvAckParams(androidProfile, androidRecv)), node.DumpDiagnostics())

	androidPayload := map[string]any{"type": 1, "content": "hello from Android text"}
	androidClientMsgNo := "android-to-ios-" + uuid.NewString()
	androidAck := requireSend(t, bob, sendParams(t, androidProfile, androidClientMsgNo, "easy-ios-alice", androidPayload), node.DumpDiagnostics())
	iosRecv := requireRecv(t, alice, "easy-android-bob", androidClientMsgNo, androidAck, androidPayload, node.DumpDiagnostics())
	require.NoError(t, alice.notify("recvack", recvAckParams(iosProfile, iosRecv)), node.DumpDiagnostics())

	// The ordered ping replies are barriers proving both preceding RECVACK
	// notifications were accepted without poisoning either session.
	requirePing(t, alice, node.DumpDiagnostics())
	requirePing(t, bob, node.DumpDiagnostics())

	require.NoError(t, bob.close(), node.DumpDiagnostics())
	requireOnlineUsers(t, httpClient, node.APIAddr(), []string{"easy-ios-alice", "easy-android-bob"}, map[string]bool{
		"easy-ios-alice":   true,
		"easy-android-bob": false,
	}, node.DumpDiagnostics)

	bobReconnected := dialEasySDKClient(t, node.WebSocketURL(), androidProfile)
	requireConnect(t, bobReconnected, connectParams(androidProfile, "easy-android-bob", "android-bob-device", "bob-token"), node.DumpDiagnostics())
	requireOnlineUsers(t, httpClient, node.APIAddr(), []string{"easy-ios-alice", "easy-android-bob"}, map[string]bool{
		"easy-ios-alice":   true,
		"easy-android-bob": true,
	}, node.DumpDiagnostics)
	requirePing(t, bobReconnected, node.DumpDiagnostics())

	require.NoError(t, bobReconnected.close(), node.DumpDiagnostics())
	require.NoError(t, alice.close(), node.DumpDiagnostics())
	requireOnlineUsers(t, httpClient, node.APIAddr(), []string{"easy-ios-alice", "easy-android-bob"}, map[string]bool{
		"easy-ios-alice":   false,
		"easy-android-bob": false,
	}, node.DumpDiagnostics)
}

type easySDKClient struct {
	conn    *websocket.Conn
	profile easySDKProfile
	queued  []rpcEnvelope
	closed  bool
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

type sendAck struct {
	messageID  string
	messageSeq uint64
}

type receivedMessage struct {
	header      map[string]any
	messageID   string
	messageSeq  uint64
	clientMsgNo string
	channelID   string
	channelType int
	fromUID     string
	payload     json.RawMessage
}

func dialEasySDKClient(t *testing.T, websocketURL string, profile easySDKProfile) *easySDKClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), clientIOTimeout)
	defer cancel()
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, websocketURL, nil)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	require.NoError(t, err, "%s dial %s", profile.name, websocketURL)
	conn.SetReadLimit(1 << 20)
	client := &easySDKClient{conn: conn, profile: profile}
	t.Cleanup(func() { _ = client.close() })
	return client
}

func (c *easySDKClient) request(method string, params any) (rpcEnvelope, error) {
	id := uuid.NewString()
	if err := c.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		return rpcEnvelope{}, err
	}

	for {
		envelope, err := c.read()
		if err != nil {
			return rpcEnvelope{}, err
		}
		if len(envelope.ID) == 0 || bytes.Equal(bytes.TrimSpace(envelope.ID), []byte("null")) {
			c.queued = append(c.queued, envelope)
			continue
		}
		var responseID string
		if err := json.Unmarshal(envelope.ID, &responseID); err != nil {
			return rpcEnvelope{}, fmt.Errorf("%s response id: %w", c.profile.name, err)
		}
		if responseID != id {
			return rpcEnvelope{}, fmt.Errorf("%s response id = %q, want %q", c.profile.name, responseID, id)
		}
		if envelope.JSONRPC != "2.0" {
			return rpcEnvelope{}, fmt.Errorf("%s response jsonrpc = %q, want 2.0", c.profile.name, envelope.JSONRPC)
		}
		if hasJSONValue(envelope.Error) {
			return rpcEnvelope{}, fmt.Errorf("%s %s error response: %s", c.profile.name, method, boundedJSON(envelope.Error))
		}
		if envelope.Result == nil {
			return rpcEnvelope{}, fmt.Errorf("%s %s response omitted both result and error", c.profile.name, method)
		}
		return envelope, nil
	}
}

func (c *easySDKClient) notify(method string, params any) error {
	return c.write(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (c *easySDKClient) notification(method string) (rpcEnvelope, error) {
	for index, envelope := range c.queued {
		if envelope.Method == method {
			c.queued = append(c.queued[:index], c.queued[index+1:]...)
			return envelope, nil
		}
	}
	for {
		envelope, err := c.read()
		if err != nil {
			return rpcEnvelope{}, err
		}
		if len(envelope.ID) != 0 && !bytes.Equal(bytes.TrimSpace(envelope.ID), []byte("null")) {
			return rpcEnvelope{}, fmt.Errorf("%s received unexpected response while waiting for %s: %s", c.profile.name, method, boundedJSON(envelope.ID))
		}
		if envelope.JSONRPC != "2.0" {
			return rpcEnvelope{}, fmt.Errorf("%s notification jsonrpc = %q, want 2.0", c.profile.name, envelope.JSONRPC)
		}
		if envelope.Method == method {
			return envelope, nil
		}
		c.queued = append(c.queued, envelope)
	}
}

func (c *easySDKClient) write(value any) error {
	if c == nil || c.conn == nil || c.closed {
		return fmt.Errorf("%s connection is closed", c.profile.name)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal %s request: %w", c.profile.name, err)
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(clientIOTimeout)); err != nil {
		return err
	}
	if err := c.conn.WriteMessage(c.profile.outboundMessageType, payload); err != nil {
		return fmt.Errorf("write %s WebSocket message: %w", c.profile.name, err)
	}
	return nil
}

func (c *easySDKClient) read() (rpcEnvelope, error) {
	if c == nil || c.conn == nil || c.closed {
		return rpcEnvelope{}, fmt.Errorf("%s connection is closed", c.profile.name)
	}
	if err := c.conn.SetReadDeadline(time.Now().Add(clientIOTimeout)); err != nil {
		return rpcEnvelope{}, err
	}
	messageType, payload, err := c.conn.ReadMessage()
	if err != nil {
		return rpcEnvelope{}, fmt.Errorf("read %s WebSocket message: %w", c.profile.name, err)
	}
	if c.profile.requireInboundText && messageType != websocket.TextMessage {
		return rpcEnvelope{}, fmt.Errorf("%s inbound WebSocket message type = %d, want text", c.profile.name, messageType)
	}
	if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
		return rpcEnvelope{}, fmt.Errorf("%s inbound WebSocket message type = %d, want text or binary", c.profile.name, messageType)
	}
	var envelope rpcEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return rpcEnvelope{}, fmt.Errorf("decode %s JSON-RPC message %q: %w", c.profile.name, boundedBytes(payload), err)
	}
	return envelope, nil
}

func (c *easySDKClient) close() error {
	if c == nil || c.conn == nil || c.closed {
		return nil
	}
	c.closed = true
	deadline := time.Now().Add(time.Second)
	controlErr := c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client disconnected"), deadline)
	closeErr := c.conn.Close()
	if controlErr != nil {
		return controlErr
	}
	return closeErr
}

func connectParams(profile easySDKProfile, uid, deviceID, token string) map[string]any {
	params := map[string]any{
		"uid":   uid,
		"token": token,
	}
	putDialect(params, profile, "deviceId", "device_id", deviceID)
	putDialect(params, profile, "deviceFlag", "device_flag", 0)
	putDialect(params, profile, "clientTimestamp", "client_timestamp", time.Now().UnixMilli())
	return params
}

func sendParams(t *testing.T, profile easySDKProfile, clientMsgNo, channelID string, payload map[string]any) map[string]any {
	t.Helper()
	wirePayload := any(payload)
	if profile.dialect == snakeCase {
		encoded, err := json.Marshal(payload)
		require.NoError(t, err)
		wirePayload = string(encoded)
	}
	params := map[string]any{"payload": wirePayload}
	putDialect(params, profile, "clientMsgNo", "client_msg_no", clientMsgNo)
	putDialect(params, profile, "channelId", "channel_id", channelID)
	putDialect(params, profile, "channelType", "channel_type", personChannelType)
	if profile.dialect == snakeCase {
		params["header"] = map[string]any{
			"no_persist": false,
			"red_dot":    true,
			"sync_once":  false,
			"dup":        false,
		}
	} else {
		params["header"] = map[string]any{"redDot": true}
	}
	return params
}

func recvAckParams(profile easySDKProfile, received receivedMessage) map[string]any {
	params := make(map[string]any)
	putDialect(params, profile, "messageId", "message_id", received.messageID)
	putDialect(params, profile, "messageSeq", "message_seq", received.messageSeq)
	if profile.dialect == snakeCase {
		params["header"] = map[string]any{
			"no_persist": headerBool(received.header, "noPersist", "no_persist"),
			"red_dot":    headerBool(received.header, "redDot", "red_dot"),
			"sync_once":  headerBool(received.header, "syncOnce", "sync_once"),
			"dup":        headerBool(received.header, "dup", "DUP"),
		}
	}
	return params
}

func requireConnect(t *testing.T, client *easySDKClient, params map[string]any, diagnostics string) {
	t.Helper()
	response, err := client.request("connect", params)
	require.NoError(t, err, diagnostics)
	result := requireJSONObject(t, response.Result, "connect result", diagnostics)
	require.Equal(t, float64(reasonSuccess), requireNumberField(t, result, client.profile, "reasonCode", "reason_code", diagnostics), diagnostics)
	requireField(t, result, client.profile, "serverVersion", "server_version", diagnostics)
	requireField(t, result, client.profile, "serverKey", "server_key", diagnostics)
	requireField(t, result, client.profile, "salt", "salt", diagnostics)
	requireField(t, result, client.profile, "timeDiff", "time_diff", diagnostics)
	requireField(t, result, client.profile, "nodeId", "node_id", diagnostics)
}

func requireSend(t *testing.T, client *easySDKClient, params map[string]any, diagnostics string) sendAck {
	t.Helper()
	response, err := client.request("send", params)
	require.NoError(t, err, diagnostics)
	result := requireJSONObject(t, response.Result, "send result", diagnostics)
	require.Equal(t, float64(reasonSuccess), requireNumberField(t, result, client.profile, "reasonCode", "reason_code", diagnostics), diagnostics)
	messageID := requireStringField(t, result, client.profile, "messageId", "message_id", diagnostics)
	messageSeq := requireUintField(t, result, client.profile, "messageSeq", "message_seq", diagnostics)
	require.NotEmpty(t, messageID, diagnostics)
	require.Positive(t, messageSeq, diagnostics)
	return sendAck{messageID: messageID, messageSeq: messageSeq}
}

func requireRecv(t *testing.T, client *easySDKClient, fromUID, clientMsgNo string, ack sendAck, wantPayload map[string]any, diagnostics string) receivedMessage {
	t.Helper()
	notification, err := client.notification("recv")
	require.NoError(t, err, diagnostics)
	params := requireJSONObject(t, notification.Params, "recv params", diagnostics)
	headerRaw := requireField(t, params, client.profile, "header", "header", diagnostics)
	header := requireJSONObject(t, headerRaw, "recv header", diagnostics)
	received := receivedMessage{
		header:      header,
		messageID:   requireStringField(t, params, client.profile, "messageId", "message_id", diagnostics),
		messageSeq:  requireUintField(t, params, client.profile, "messageSeq", "message_seq", diagnostics),
		clientMsgNo: requireStringField(t, params, client.profile, "clientMsgNo", "client_msg_no", diagnostics),
		channelID:   requireStringField(t, params, client.profile, "channelId", "channel_id", diagnostics),
		channelType: int(requireNumberField(t, params, client.profile, "channelType", "channel_type", diagnostics)),
		fromUID:     requireStringField(t, params, client.profile, "fromUid", "from_uid", diagnostics),
		payload:     requireField(t, params, client.profile, "payload", "payload", diagnostics),
	}
	require.Equal(t, ack.messageID, received.messageID, diagnostics)
	require.Equal(t, ack.messageSeq, received.messageSeq, diagnostics)
	require.Equal(t, clientMsgNo, received.clientMsgNo, diagnostics)
	require.Equal(t, fromUID, received.channelID, diagnostics)
	require.Equal(t, personChannelType, received.channelType, diagnostics)
	require.Equal(t, fromUID, received.fromUID, diagnostics)
	trimmedPayload := bytes.TrimSpace(received.payload)
	require.NotEmpty(t, trimmedPayload, diagnostics)
	require.Equal(t, byte('{'), trimmedPayload[0], "%s: payload must remain a direct JSON object, not a base64 string", diagnostics)
	wantPayloadJSON, err := json.Marshal(wantPayload)
	require.NoError(t, err)
	require.JSONEq(t, string(wantPayloadJSON), string(received.payload), diagnostics)
	return received
}

func requirePing(t *testing.T, client *easySDKClient, diagnostics string) {
	t.Helper()
	response, err := client.request("ping", map[string]any{})
	require.NoError(t, err, diagnostics)
	require.NotNil(t, response.Result, "%s ping response must contain a JSON-RPC result member\n%s", client.profile.name, diagnostics)
	require.True(t, json.Valid(response.Result), "%s ping result must be valid JSON: %q\n%s", client.profile.name, response.Result, diagnostics)
}

func requireOnlineUsers(t *testing.T, client *http.Client, apiAddr string, uids []string, want map[string]bool, diagnostics func() string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), clientIOTimeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	var lastBody string
	var lastErr error
	for {
		statuses, body, err := fetchOnlineUsers(ctx, client, apiAddr, uids)
		lastBody, lastErr = body, err
		if err == nil {
			matches := true
			for uid, expectedOnline := range want {
				if statuses[uid] != expectedOnline {
					matches = false
					break
				}
			}
			if matches {
				return
			}
		}

		select {
		case <-ctx.Done():
			t.Fatalf("public /user/onlinestatus did not converge: want=%v last_body=%q last_err=%v\n%s", want, lastBody, lastErr, diagnostics())
		case <-ticker.C:
		}
	}
}

func fetchOnlineUsers(parent context.Context, client *http.Client, apiAddr string, uids []string) (map[string]bool, string, error) {
	body, err := json.Marshal(uids)
	if err != nil {
		return nil, "", err
	}
	request, err := http.NewRequestWithContext(parent, http.MethodPost, "http://"+apiAddr+"/user/onlinestatus", bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	if err != nil {
		return nil, "", err
	}
	if response.StatusCode != http.StatusOK {
		return nil, string(responseBody), fmt.Errorf("status = %d", response.StatusCode)
	}
	var entries []struct {
		UID    string `json:"uid"`
		Online int    `json:"online"`
	}
	if err := json.Unmarshal(responseBody, &entries); err != nil {
		return nil, string(responseBody), err
	}
	online := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.Online == 1 {
			online[entry.UID] = true
		}
	}
	return online, string(responseBody), nil
}

func putDialect(target map[string]any, profile easySDKProfile, camel, snake string, value any) {
	if profile.dialect == snakeCase {
		target[snake] = value
		return
	}
	target[camel] = value
}

func requireField(t *testing.T, object map[string]any, profile easySDKProfile, camel, snake, diagnostics string) json.RawMessage {
	t.Helper()
	keys := []string{camel}
	if profile.dialect == snakeCase {
		keys = []string{snake, camel}
	}
	for _, key := range keys {
		if value, ok := object[key]; ok {
			encoded, err := json.Marshal(value)
			require.NoError(t, err, diagnostics)
			return encoded
		}
	}
	t.Fatalf("%s response omitted %s field (accepted keys %v)\n%s", profile.name, camel, keys, diagnostics)
	return nil
}

func requireStringField(t *testing.T, object map[string]any, profile easySDKProfile, camel, snake, diagnostics string) string {
	t.Helper()
	raw := requireField(t, object, profile, camel, snake, diagnostics)
	var value string
	require.NoError(t, json.Unmarshal(raw, &value), "%s field %s must be a string\n%s", profile.name, camel, diagnostics)
	return value
}

func requireNumberField(t *testing.T, object map[string]any, profile easySDKProfile, camel, snake, diagnostics string) float64 {
	t.Helper()
	raw := requireField(t, object, profile, camel, snake, diagnostics)
	var value float64
	require.NoError(t, json.Unmarshal(raw, &value), "%s field %s must be numeric\n%s", profile.name, camel, diagnostics)
	return value
}

func requireUintField(t *testing.T, object map[string]any, profile easySDKProfile, camel, snake, diagnostics string) uint64 {
	t.Helper()
	value := requireNumberField(t, object, profile, camel, snake, diagnostics)
	require.GreaterOrEqual(t, value, float64(0), diagnostics)
	require.Equal(t, value, float64(uint64(value)), "%s field %s must be an integer\n%s", profile.name, camel, diagnostics)
	return uint64(value)
}

func requireJSONObject(t *testing.T, raw json.RawMessage, label, diagnostics string) map[string]any {
	t.Helper()
	var object map[string]any
	require.NoError(t, json.Unmarshal(raw, &object), "%s must be a JSON object: %q\n%s", label, boundedJSON(raw), diagnostics)
	require.NotNil(t, object, "%s must be a JSON object\n%s", label, diagnostics)
	return object
}

func headerBool(header map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := header[key].(bool); ok {
			return value
		}
	}
	return false
}

func hasJSONValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func boundedJSON(raw json.RawMessage) string {
	return boundedBytes(raw)
}

func boundedBytes(value []byte) string {
	const limit = 512
	if len(value) <= limit {
		return string(value)
	}
	return string(value[:limit]) + "..."
}
