//go:build e2e

package suite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPostConversationListDecodesPublicResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/conversation/list", r.URL.Path)

		var req map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "u1", req["uid"])
		require.Equal(t, float64(10), req["limit"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"conversations":[{"channel_id":"g1","channel_type":2,"sparse_active":false,"last_message":{"message_id":7,"message_seq":3,"from_uid":"u2","client_msg_no":"c1","payload":"aGVsbG8="}}],"more":0}`))
	}))
	defer server.Close()

	page, err := PostConversationList(context.Background(), strings.TrimPrefix(server.URL, "http://"), "u1", 10)
	require.NoError(t, err)
	require.Len(t, page.Conversations, 1)
	require.Equal(t, "g1", page.Conversations[0].ChannelID)
	require.Equal(t, []byte("hello"), page.Conversations[0].LastMessage.Payload)
}

func TestGetJSONDecodesPublicResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/manager/slots", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":1}`))
	}))
	defer server.Close()

	var out struct {
		Total int `json:"total"`
	}
	_, err := GetJSON(context.Background(), server.URL+"/manager/slots", &out)
	require.NoError(t, err)
	require.Equal(t, 1, out.Total)
}

func TestParseMetricSampleMatchesLabels(t *testing.T) {
	name, labels, value, ok := parseMetricSample(`wukongim_authority_recipient_dispatch_total{phase="conversation",result="ok"} 3`)

	require.True(t, ok)
	require.Equal(t, "wukongim_authority_recipient_dispatch_total", name)
	require.Equal(t, map[string]string{"phase": "conversation", "result": "ok"}, labels)
	require.Equal(t, float64(3), value)
	require.True(t, metricLabelsMatch(labels, map[string]string{"result": "ok"}))
	require.False(t, metricLabelsMatch(labels, map[string]string{"result": "error"}))
}
