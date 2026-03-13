package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/health", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	err := healthCmd.RunE(healthCmd, nil)
	assert.NoError(t, err)
}

func TestHealthCmd_ServerDown(t *testing.T) {
	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = "http://localhost:1" // Non-existent server.

	err := healthCmd.RunE(healthCmd, nil)
	assert.Error(t, err)
}

func TestHealthCmd_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"code":500,"msg":"unhealthy"}`))
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	err := healthCmd.RunE(healthCmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unhealthy")
}

func TestUserTokenCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/user/token", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)

		b, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(b, &req)
		assert.Equal(t, "testuid", req["uid"])
		assert.Equal(t, "testtoken", req["token"])
		assert.Equal(t, float64(1), req["device_flag"])
		assert.Equal(t, float64(1), req["device_level"])

		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	cmd := userTokenCmd
	cmd.Flags().Set("uid", "testuid")
	cmd.Flags().Set("token", "testtoken")
	cmd.Flags().Set("device_flag", "1")
	cmd.Flags().Set("device_level", "1")

	err := cmd.RunE(cmd, nil)
	assert.NoError(t, err)
}

func TestUserOnlineCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/user/onlinestatus", r.URL.Path)

		b, _ := io.ReadAll(r.Body)
		var uids []string
		json.Unmarshal(b, &uids)
		assert.Equal(t, []string{"u1", "u2"}, uids)

		w.WriteHeader(200)
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{"uid": "u1", "device_flag": 0, "online": 1},
			{"uid": "u2", "device_flag": 1, "online": 0},
		})
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	cmd := userOnlineCmd
	cmd.Flags().Set("uids", "u1,u2")

	err := cmd.RunE(cmd, nil)
	assert.NoError(t, err)
}

func TestChannelCreateCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/channel", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)

		b, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(b, &req)
		assert.Equal(t, "testgroup", req["channel_id"])
		assert.Equal(t, float64(2), req["channel_type"])

		subs, ok := req["subscribers"].([]interface{})
		require.True(t, ok)
		assert.Len(t, subs, 2)

		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	cmd := channelCreateCmd
	cmd.Flags().Set("channel_id", "testgroup")
	cmd.Flags().Set("channel_type", "2")
	cmd.Flags().Set("subscribers", "u1,u2")

	err := cmd.RunE(cmd, nil)
	assert.NoError(t, err)
}

func TestChannelDeleteCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/channel/delete", r.URL.Path)

		b, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(b, &req)
		assert.Equal(t, "testgroup", req["channel_id"])

		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	cmd := channelDeleteCmd
	cmd.Flags().Set("channel_id", "testgroup")

	err := cmd.RunE(cmd, nil)
	assert.NoError(t, err)
}

func TestSubscribersAddCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/channel/subscriber_add", r.URL.Path)

		b, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(b, &req)
		assert.Equal(t, "testgroup", req["channel_id"])

		subs, ok := req["subscribers"].([]interface{})
		require.True(t, ok)
		assert.Len(t, subs, 3)

		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	cmd := subscribersAddCmd
	cmd.Flags().Set("channel_id", "testgroup")
	cmd.Flags().Set("uids", "u1,u2,u3")

	err := cmd.RunE(cmd, nil)
	assert.NoError(t, err)
}

func TestSubscribersRemoveCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/channel/subscriber_remove", r.URL.Path)

		b, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(b, &req)

		subs, ok := req["subscribers"].([]interface{})
		require.True(t, ok)
		assert.Len(t, subs, 1)
		assert.Equal(t, "u1", subs[0])

		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	cmd := subscribersRemoveCmd
	cmd.Flags().Set("channel_id", "testgroup")
	cmd.Flags().Set("uids", "u1")

	err := cmd.RunE(cmd, nil)
	assert.NoError(t, err)
}

func TestMessageSendCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/message/send", r.URL.Path)

		b, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(b, &req)
		assert.Equal(t, "user1", req["from_uid"])
		assert.Equal(t, "group1", req["channel_id"])

		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"data": map[string]interface{}{
				"message_id":   1234567890,
				"client_msg_no": "abc-123",
			},
		})
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	cmd := messageSendCmd
	cmd.Flags().Set("from", "user1")
	cmd.Flags().Set("channel_id", "group1")
	cmd.Flags().Set("channel_type", "2")
	cmd.Flags().Set("payload", `{"content":"hello"}`)

	err := cmd.RunE(cmd, nil)
	assert.NoError(t, err)
}

func TestMessageSyncCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/channel/messagesync", r.URL.Path)

		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"data": map[string]interface{}{
				"start_message_seq": 0,
				"end_message_seq":   10,
				"more":              0,
				"messages": []map[string]interface{}{
					{
						"message_id":   123,
						"from_uid":     "user1",
						"channel_id":   "group1",
						"channel_type": 2,
						"payload":      map[string]string{"content": "hello"},
						"seq":          1,
						"timestamp":    1700000000,
					},
				},
			},
		})
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	cmd := messageSyncCmd
	cmd.Flags().Set("channel_id", "group1")
	cmd.Flags().Set("channel_type", "2")

	err := cmd.RunE(cmd, nil)
	assert.NoError(t, err)
}

func TestMessageSyncCmd_EmptyMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"data": map[string]interface{}{
				"messages": []interface{}{},
			},
		})
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	cmd := messageSyncCmd
	cmd.Flags().Set("channel_id", "group1")

	err := cmd.RunE(cmd, nil)
	assert.NoError(t, err)
}

func TestServerVarzCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/varz", r.URL.Path)

		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"server_id":   "1",
			"version":     "2.0.0",
			"uptime":      "1d2h",
			"connections": 100,
			"cpu":         45.5,
			"goroutine":   500,
			"mem":         1024 * 1024 * 512,
			"in_msgs":     10000,
			"out_msgs":    20000,
			"in_bytes":    5000000,
			"out_bytes":   10000000,
			"tcp_addr":    "0.0.0.0:5200",
			"ws_addr":     "0.0.0.0:5201",
			"commit":      "abc123",
		})
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	err := serverVarzCmd.RunE(serverVarzCmd, nil)
	assert.NoError(t, err)
}

func TestServerConnzCmd_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/connz", r.URL.Path)
		assert.Equal(t, "20", r.URL.Query().Get("limit"))

		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"total":  2,
			"offset": 0,
			"limit":  20,
			"connections": []map[string]interface{}{
				{
					"id": 1, "uid": "user1", "ip": "192.168.1.1",
					"port": 5000, "uptime": "2h", "device": "App",
					"in_msgs": 100, "out_msgs": 200,
				},
				{
					"id": 2, "uid": "user2", "ip": "192.168.1.2",
					"port": 5001, "uptime": "1h", "device": "Web",
					"in_msgs": 50, "out_msgs": 80,
				},
			},
		})
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	err := serverConnzCmd.RunE(serverConnzCmd, nil)
	assert.NoError(t, err)
}

func TestServerConnzCmd_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"total":       0,
			"offset":      0,
			"limit":       20,
			"connections": []interface{}{},
		})
	}))
	defer server.Close()

	oldServer := flagServer
	defer func() { flagServer = oldServer }()
	flagServer = server.URL

	err := serverConnzCmd.RunE(serverConnzCmd, nil)
	assert.NoError(t, err)
}
