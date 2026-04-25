//go:build integration
// +build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestAppConversationSyncReturnsLegacyConversationAfterSend(t *testing.T) {
	cfg := testConfig(t)
	cfg.API.ListenAddr = "127.0.0.1:0"

	app, err := New(cfg)
	require.NoError(t, err)
	channelID := deliveryusecase.EncodePersonChannel("u1", "u2")
	seedChannelRuntimeMeta(t, app, channelID, frame.ChannelTypePerson)

	require.NoError(t, app.Start())
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	_, err = app.Message().Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "conversation-sync-1",
		Payload:     []byte("hello sync"),
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{"uid":"u2","limit":10}`))
		req.Header.Set("Content-Type", "application/json")

		app.API().Engine().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			return false
		}

		var got []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			return false
		}
		if len(got) != 1 {
			return false
		}
		return got[0]["channel_id"] == "u1" &&
			got[0]["last_client_msg_no"] == "conversation-sync-1" &&
			got[0]["last_msg_seq"] == float64(1) &&
			got[0]["unread"] == float64(1)
	}, 3*time.Second, 20*time.Millisecond)
}

func TestConversationSyncLoadsFactsFromRemoteOwnerWhenAPINodeIsNotReplica(t *testing.T) {
	harness := newThreeNodeConversationSyncHarness(t)
	groupLeaderID := harness.waitForStableLeader(t, 1)
	groupLeader := harness.apps[groupLeaderID]
	apiNode := harness.apps[1]

	senderUID := "remote-owner-sender"
	recipientUID := "remote-owner-recipient"
	channelID := deliveryusecase.EncodePersonChannel(senderUID, recipientUID)
	id := channel.ChannelID{
		ID:   channelID,
		Type: frame.ChannelTypePerson,
	}

	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 15,
		LeaderEpoch:  6,
		Replicas:     []uint64{2, 3},
		ISR:          []uint64{2, 3},
		Leader:       2,
		MinISR:       1,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, groupLeader.Store().UpsertChannelRuntimeMeta(context.Background(), meta))
	for _, nodeID := range []uint64{2, 3} {
		_, err := harness.apps[nodeID].channelMetaSync.RefreshChannelMeta(context.Background(), id)
		require.NoError(t, err)
	}

	_, err := harness.apps[2].Message().Send(context.Background(), message.SendCommand{
		FromUID:     senderUID,
		ChannelID:   recipientUID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "conversation-sync-remote-1",
		Payload:     []byte("hello remote sync"),
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(fmt.Sprintf(`{"uid":"%s","limit":10,"msg_count":1}`, recipientUID)))
		req.Header.Set("Content-Type", "application/json")

		apiNode.API().Engine().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			return false
		}

		var got []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			return false
		}
		if len(got) != 1 {
			return false
		}
		recents, ok := got[0]["recents"].([]any)
		if !ok || len(recents) != 1 {
			return false
		}
		recent0, ok := recents[0].(map[string]any)
		if !ok {
			return false
		}
		return got[0]["channel_id"] == senderUID &&
			got[0]["last_client_msg_no"] == "conversation-sync-remote-1" &&
			got[0]["last_msg_seq"] == float64(1) &&
			recent0["client_msg_no"] == "conversation-sync-remote-1"
	}, 5*time.Second, 20*time.Millisecond)
}
