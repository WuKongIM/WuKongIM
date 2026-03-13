package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

func TestExtractContent_EmptyPayload(t *testing.T) {
	assert.Equal(t, "(empty)", extractContent(nil))
	assert.Equal(t, "(empty)", extractContent([]byte{}))
}

func TestExtractContent_ContentField(t *testing.T) {
	payload := []byte(`{"content":"hello world"}`)
	assert.Equal(t, "hello world", extractContent(payload))
}

func TestExtractContent_TextField(t *testing.T) {
	payload := []byte(`{"text":"hi there"}`)
	assert.Equal(t, "hi there", extractContent(payload))
}

func TestExtractContent_ContentPriority(t *testing.T) {
	// When both "content" and "text" exist, "content" takes priority.
	payload := []byte(`{"content":"from content","text":"from text"}`)
	assert.Equal(t, "from content", extractContent(payload))
}

func TestExtractContent_OtherJSON(t *testing.T) {
	payload := []byte(`{"type":"image","url":"https://example.com/img.png"}`)
	result := extractContent(payload)
	assert.Contains(t, result, "image")
	assert.Contains(t, result, "https://example.com/img.png")
}

func TestExtractContent_RawString(t *testing.T) {
	payload := []byte("plain text message")
	assert.Equal(t, "plain text message", extractContent(payload))
}

func TestChatSession_NextID(t *testing.T) {
	session := &chatSession{}

	id1 := session.nextID()
	id2 := session.nextID()
	id3 := session.nextID()

	assert.Equal(t, "1", id1)
	assert.Equal(t, "2", id2)
	assert.Equal(t, "3", id3)
}

func TestChatSession_HandleInput_Quit(t *testing.T) {
	session := &chatSession{
		channelID:   "test",
		channelType: 2,
	}

	err := session.handleInput("/quit")
	assert.Error(t, err)
	assert.Equal(t, "quit", err.Error())
}

func TestChatSession_HandleInput_NoChannel(t *testing.T) {
	session := &chatSession{
		channelID: "", // No channel.
	}

	// Should not return error, just print warning.
	err := session.handleInput("hello")
	assert.NoError(t, err)
}

func TestGetWSAddr_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/route", r.URL.Path)
		assert.Equal(t, "testuser", r.URL.Query().Get("uid"))
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{
			"ws_addr": "localhost:5200",
		})
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	addr, err := getWSAddr("testuser")
	assert.NoError(t, err)
	assert.Equal(t, "localhost:5200", addr)
}

func TestGetWSAddr_EmptyWSAddr(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{
			"ws_addr": "",
		})
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	_, err := getWSAddr("testuser")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no ws_addr")
}

func TestGetWSAddr_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"code":500,"msg":"server error"}`))
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	_, err := getWSAddr("testuser")
	assert.Error(t, err)
}

func TestChatSession_HandleServerMessage_RecvMethod(t *testing.T) {
	// Set up a mock WebSocket server so recvack doesn't panic on nil conn.
	upgrader := websocket.Upgrader{}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Read the recvack message the client will send.
		conn.ReadMessage()
	}))
	defer wsServer.Close()

	wsURL := "ws" + wsServer.URL[4:] // http -> ws
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	assert.NoError(t, err)
	defer conn.Close()

	session := &chatSession{
		conn: conn,
		uid:  "user1",
		done: make(chan struct{}),
	}

	msg := `{"jsonrpc":"2.0","method":"recv","params":{"messageId":"123","messageSeq":1,"fromUid":"user2","channelId":"group1","channelType":2,"timestamp":1700000000,"payload":"eyJjb250ZW50IjoiaGVsbG8ifQ=="}}`

	assert.NotPanics(t, func() {
		session.handleServerMessage([]byte(msg))
	})
}

func TestChatSession_HandleServerMessage_EmptyMethod(t *testing.T) {
	session := &chatSession{
		uid:  "user1",
		done: make(chan struct{}),
	}

	// Response message (no method) should be handled silently.
	msg := `{"jsonrpc":"2.0","id":"1","result":null}`
	assert.NotPanics(t, func() {
		session.handleServerMessage([]byte(msg))
	})
}

func TestChatSession_HandleServerMessage_InvalidJSON(t *testing.T) {
	session := &chatSession{
		uid:  "user1",
		done: make(chan struct{}),
	}

	// Invalid JSON should not panic.
	assert.NotPanics(t, func() {
		session.handleServerMessage([]byte("not json"))
	})
}

func TestChatSession_HandleServerMessage_Disconnect(t *testing.T) {
	session := &chatSession{
		uid:  "user1",
		done: make(chan struct{}),
	}

	msg := `{"jsonrpc":"2.0","method":"disconnect","params":{"reasonCode":1,"reason":"kicked"}}`
	assert.NotPanics(t, func() {
		session.handleServerMessage([]byte(msg))
	})
}
