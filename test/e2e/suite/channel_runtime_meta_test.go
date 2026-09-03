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

func TestGetChannelRuntimeMetaUsesExactManagerLookup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/manager/channel-runtime-meta", r.URL.Path)
		require.Equal(t, "1", r.URL.Query().Get("exact"))
		require.Equal(t, "room-1", r.URL.Query().Get("channel_id"))
		require.Equal(t, "2", r.URL.Query().Get("channel_type"))
		require.NoError(t, json.NewEncoder(w).Encode(channelRuntimeMetaPage{Items: []ChannelRuntimeMeta{{
			ChannelID: "room-1", ChannelType: 2, Leader: 3, Status: "active",
		}}}))
	}))
	defer server.Close()

	node := &StartedNode{Spec: NodeSpec{ManagerAddr: strings.TrimPrefix(server.URL, "http://")}}
	meta, err := GetChannelRuntimeMeta(context.Background(), node, "room-1", 2)

	require.NoError(t, err)
	require.Equal(t, uint64(3), meta.Leader)
	require.Equal(t, "active", meta.Status)
}
