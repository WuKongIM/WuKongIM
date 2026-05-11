package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestAPIServerSendMessageWithRealMessageApp(t *testing.T) {
	cluster := &fakeChannelCluster{
		result: channel.AppendResult{MessageID: 66, MessageSeq: 7},
	}
	msgApp := message.New(message.Options{
		IdentityStore: &fakeIdentityStore{},
		ChannelStore:  &fakeChannelStore{},
		MetaRefresher: &fakeMetaRefresher{},
		Cluster:       cluster,
	})
	require.NoError(t, msgApp.OnlineRegistry().Register(online.OnlineConn{
		SessionID: 2,
		UID:       "u2",
		Session:   apiTestSession{id: 2, listener: "api"},
	}))

	srv := New(Options{
		ListenAddr: "127.0.0.1:0",
		Messages:   msgApp,
	})
	require.NoError(t, srv.Start())
	t.Cleanup(func() {
		require.NoError(t, srv.Stop(context.Background()))
	})

	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + srv.Addr() + "/healthz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 2*time.Second, 20*time.Millisecond)

	body := map[string]any{
		"from_uid":     "u1",
		"channel_id":   "u2",
		"channel_type": float64(frame.ChannelTypePerson),
		"payload":      base64.StdEncoding.EncodeToString([]byte("hi")),
		"sync_once":    float64(1),
	}
	payload, err := json.Marshal(body)
	require.NoError(t, err)

	resp, err := http.Post("http://"+srv.Addr()+"/message/send", "application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		MessageID  int64  `json:"message_id"`
		MessageSeq uint64 `json:"message_seq"`
		Reason     uint8  `json:"reason"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, int64(66), got.MessageID)
	require.Equal(t, uint64(7), got.MessageSeq)
	require.Equal(t, uint8(frame.ReasonSuccess), got.Reason)
	require.Len(t, cluster.appendRequests, 1)
	require.Equal(t, runtimechannelid.ToCommandChannel(runtimechannelid.EncodePersonChannel("u1", "u2")), cluster.appendRequests[0].ChannelID.ID)
	require.True(t, cluster.appendRequests[0].Message.Framer.SyncOnce)
}

func TestAPIServerSendMessageNoPersistHeaderSkipsClusterRequirement(t *testing.T) {
	msgApp := message.New(message.Options{})
	srv := New(Options{
		ListenAddr: "127.0.0.1:0",
		Messages:   msgApp,
	})
	require.NoError(t, srv.Start())
	t.Cleanup(func() {
		require.NoError(t, srv.Stop(context.Background()))
	})

	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + srv.Addr() + "/healthz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 2*time.Second, 20*time.Millisecond)

	body := map[string]any{
		"from_uid":     "u1",
		"channel_id":   "u2",
		"channel_type": float64(frame.ChannelTypePerson),
		"payload":      base64.StdEncoding.EncodeToString([]byte("hi")),
		"header": map[string]any{
			"no_persist": float64(1),
		},
	}
	payload, err := json.Marshal(body)
	require.NoError(t, err)

	resp, err := http.Post("http://"+srv.Addr()+"/message/send", "application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		MessageID  int64  `json:"message_id"`
		MessageSeq uint64 `json:"message_seq"`
		Reason     uint8  `json:"reason"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Zero(t, got.MessageID)
	require.Zero(t, got.MessageSeq)
	require.Equal(t, uint8(frame.ReasonSuccess), got.Reason)
}

type apiTestSession struct {
	id       uint64
	listener string
}

func (s apiTestSession) ID() uint64                   { return s.id }
func (s apiTestSession) Listener() string             { return s.listener }
func (s apiTestSession) RemoteAddr() string           { return "" }
func (s apiTestSession) LocalAddr() string            { return "" }
func (s apiTestSession) WriteFrame(frame.Frame) error { return nil }
func (s apiTestSession) Close() error                 { return nil }
func (s apiTestSession) SetValue(string, any)         {}
func (s apiTestSession) Value(string) any             { return nil }

type fakeIdentityStore struct{}

func (*fakeIdentityStore) GetUser(context.Context, string) (metadb.User, error) {
	return metadb.User{}, nil
}

type fakeChannelStore struct{}

func (*fakeChannelStore) GetChannel(context.Context, string, int64) (metadb.Channel, error) {
	return metadb.Channel{}, nil
}

type fakeChannelCluster struct {
	appendRequests []channel.AppendRequest
	result         channel.AppendResult
	err            error
}

type fakeMetaRefresher struct{}

func (*fakeChannelCluster) ApplyMeta(channel.Meta) error {
	return nil
}

func (f *fakeChannelCluster) Append(_ context.Context, req channel.AppendRequest) (channel.AppendResult, error) {
	f.appendRequests = append(f.appendRequests, req)
	return f.result, f.err
}

func (*fakeMetaRefresher) RefreshChannelMeta(context.Context, channel.ChannelID) (channel.Meta, error) {
	return channel.Meta{}, nil
}
