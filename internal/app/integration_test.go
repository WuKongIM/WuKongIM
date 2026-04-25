//go:build integration
// +build integration

package app

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestAppStartAcceptsWKProtoConnectionAndStopsCleanly(t *testing.T) {
	cfg := testConfig(t)

	app, err := New(cfg)
	require.NoError(t, err)

	require.NoError(t, app.Start())
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	conn, err := net.Dial("tcp", app.Gateway().ListenerAddr("tcp-wkproto"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	sendAppWKProtoFrame(t, conn, &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             "app-user",
		DeviceID:        "app-device",
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	})

	pkt := readAppWKProtoFrame(t, conn)
	connack, ok := pkt.(*frame.ConnackPacket)
	require.True(t, ok, "expected *frame.ConnackPacket, got %T", pkt)
	require.Equal(t, frame.ReasonSuccess, connack.ReasonCode)
}

func TestAppStartPreloadsLocalChannelRuntimeMeta(t *testing.T) {
	cfg := testConfig(t)

	app, err := New(cfg)
	require.NoError(t, err)

	id := channel.ChannelID{ID: "preload-user", Type: 1}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 3,
		LeaderEpoch:  4,
		Replicas:     []uint64{cfg.Node.ID},
		ISR:          []uint64{cfg.Node.ID},
		Leader:       cfg.Node.ID,
		MinISR:       1,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, app.DB().ForSlot(1).UpsertChannelRuntimeMeta(context.Background(), meta))

	require.NoError(t, app.Start())
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	status, err := app.ChannelLog().Status(id)
	require.NoError(t, err)
	require.Equal(t, channelhandler.KeyFromChannelID(id), status.Key)
	require.Equal(t, id, status.ID)
	require.Equal(t, channel.StatusActive, status.Status)
	require.Equal(t, channel.NodeID(cfg.Node.ID), status.Leader)
	require.Equal(t, uint64(4), status.LeaderEpoch)
}

func TestAppStartWiresMessageSendThroughDurableChannelLog(t *testing.T) {
	cfg := testConfig(t)

	app, err := New(cfg)
	require.NoError(t, err)

	id := channel.ChannelID{
		ID:   deliveryusecase.EncodePersonChannel("sender", "durable-user"),
		Type: frame.ChannelTypePerson,
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 9,
		LeaderEpoch:  10,
		Replicas:     []uint64{cfg.Node.ID},
		ISR:          []uint64{cfg.Node.ID},
		Leader:       cfg.Node.ID,
		MinISR:       1,
		Status:       uint8(channel.StatusActive),
		Features:     uint64(channel.MessageSeqFormatLegacyU32),
		LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
	}
	require.NoError(t, app.DB().ForSlot(1).UpsertChannelRuntimeMeta(context.Background(), meta))

	require.NoError(t, app.Start())
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	result, err := app.Message().Send(context.Background(), message.SendCommand{
		FromUID:     "sender",
		ChannelID:   "durable-user",
		ChannelType: id.Type,
		ClientMsgNo: "durable-1",
		Payload:     []byte("hello durable"),
	})
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.NotZero(t, result.MessageID)
	require.Equal(t, uint64(1), result.MessageSeq)

	fetch, err := app.ChannelLog().Fetch(context.Background(), channel.FetchRequest{
		ChannelID: id,
		FromSeq:   1,
		Limit:     10,
		MaxBytes:  1024,
	})
	require.NoError(t, err)
	require.Len(t, fetch.Messages, 1)
	require.Equal(t, uint64(1), fetch.Messages[0].MessageSeq)
	require.Equal(t, []byte("hello durable"), fetch.Messages[0].Payload)
}

func TestAppStartBootstrapsMissingRuntimeMetaOnFirstSend(t *testing.T) {
	cfg := testConfig(t)

	app, err := New(cfg)
	require.NoError(t, err)

	require.NoError(t, app.Start())
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})
	require.Eventually(t, func() bool {
		_, err := app.Cluster().LeaderOf(1)
		return err == nil
	}, 3*time.Second, 50*time.Millisecond)

	id := channel.ChannelID{
		ID:   deliveryusecase.EncodePersonChannel("sender", "bootstrap-user"),
		Type: frame.ChannelTypePerson,
	}
	_, err = app.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
	require.ErrorIs(t, err, metadb.ErrNotFound)

	result, err := app.Message().Send(context.Background(), message.SendCommand{
		FromUID:     "sender",
		ChannelID:   "bootstrap-user",
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "bootstrap-first-send",
		Payload:     []byte("hello bootstrap"),
	})
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.NotZero(t, result.MessageID)
	require.Equal(t, uint64(1), result.MessageSeq)

	meta, err := app.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
	require.NoError(t, err)
	require.Equal(t, id.ID, meta.ChannelID)
	require.Equal(t, int64(id.Type), meta.ChannelType)
	require.Equal(t, []uint64{cfg.Node.ID}, meta.Replicas)
	require.Equal(t, []uint64{cfg.Node.ID}, meta.ISR)
	require.Equal(t, cfg.Node.ID, meta.Leader)

	fetch, err := app.ChannelLog().Fetch(context.Background(), channel.FetchRequest{
		ChannelID: id,
		FromSeq:   1,
		Limit:     10,
		MaxBytes:  1024,
	})
	require.NoError(t, err)
	require.Len(t, fetch.Messages, 1)
	require.Equal(t, uint64(1), fetch.Messages[0].MessageSeq)
	require.Equal(t, []byte("hello bootstrap"), fetch.Messages[0].Payload)
}

func TestAppRuntimeMetaReadMissDoesNotBootstrap(t *testing.T) {
	cfg := testConfig(t)

	app, err := New(cfg)
	require.NoError(t, err)

	require.NoError(t, app.Start())
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})
	require.Eventually(t, func() bool {
		_, err := app.Cluster().LeaderOf(1)
		return err == nil
	}, 3*time.Second, 50*time.Millisecond)

	id := channel.ChannelID{
		ID:   "read-miss-group",
		Type: frame.ChannelTypeGroup,
	}
	key := channelhandler.KeyFromChannelID(id)

	_, err = app.ChannelLog().Status(id)
	require.ErrorIs(t, err, channel.ErrStaleMeta)
	_, ok := app.channelLog.MetaSnapshot(key)
	require.False(t, ok)

	_, err = app.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
	require.ErrorIs(t, err, metadb.ErrNotFound)

	_, err = app.ChannelLog().Status(id)
	require.ErrorIs(t, err, channel.ErrStaleMeta)
	_, ok = app.channelLog.MetaSnapshot(key)
	require.False(t, ok)

	_, err = app.Store().GetChannelRuntimeMeta(context.Background(), id.ID, int64(id.Type))
	require.ErrorIs(t, err, metadb.ErrNotFound)
}

func TestAppSendReturnsBeforeRealtimeAckArrives(t *testing.T) {
	cfg := testConfig(t)
	channelID := deliveryusecase.EncodePersonChannel("u1", "u2")

	app, err := New(cfg)
	require.NoError(t, err)
	seedChannelRuntimeMeta(t, app, channelID, frame.ChannelTypePerson)

	require.NoError(t, app.Start())
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	recipientConn := connectAppWKProtoClient(t, app, "u2")
	t.Cleanup(func() { _ = recipientConn.Close() })

	result, err := app.Message().Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "async-1",
		Payload:     []byte("hi async"),
	})

	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.NotZero(t, result.MessageID)
	require.NotZero(t, result.MessageSeq)

	recv := readAppRecvPacket(t, recipientConn)
	require.Equal(t, "u1", recv.FromUID)
	require.Equal(t, "u1", recv.ChannelID)
	require.Equal(t, frame.ChannelTypePerson, recv.ChannelType)
	require.Equal(t, []byte("hi async"), recv.Payload)
	require.Equal(t, uint64(result.MessageID), uint64(recv.MessageID))
	require.Equal(t, result.MessageSeq, recv.MessageSeq)

	sendAppWKProtoFrame(t, recipientConn, &frame.RecvackPacket{
		MessageID:  recv.MessageID,
		MessageSeq: recv.MessageSeq,
	})
}

func TestAppRecvAckCompletesLocalInflightRoute(t *testing.T) {
	cfg := testConfig(t)
	channelID := deliveryusecase.EncodePersonChannel("u1", "u2")

	app, err := New(cfg)
	require.NoError(t, err)
	seedChannelRuntimeMeta(t, app, channelID, frame.ChannelTypePerson)

	require.NoError(t, app.Start())
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	recipientConn := connectAppWKProtoClient(t, app, "u2")
	t.Cleanup(func() { _ = recipientConn.Close() })

	sessionID := waitForPresenceSessionID(t, app, "u2")

	result, err := app.Message().Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "async-ack-1",
		Payload:     []byte("hi ack"),
	})
	require.NoError(t, err)

	recv := readAppRecvPacket(t, recipientConn)
	require.Equal(t, result.MessageSeq, recv.MessageSeq)
	require.Eventually(t, func() bool {
		return app.deliveryRuntime.HasAckBinding(sessionID, uint64(recv.MessageID))
	}, time.Second, 10*time.Millisecond)

	sendAppWKProtoFrame(t, recipientConn, &frame.RecvackPacket{
		MessageID:  recv.MessageID,
		MessageSeq: recv.MessageSeq,
	})

	require.Eventually(t, func() bool {
		return !app.deliveryRuntime.HasAckBinding(sessionID, uint64(recv.MessageID))
	}, time.Second, 10*time.Millisecond)
}

func TestAppSessionCloseDropsRealtimeRouteAndDoesNotBlockSend(t *testing.T) {
	cfg := testConfig(t)
	channelID := deliveryusecase.EncodePersonChannel("u1", "u2")

	app, err := New(cfg)
	require.NoError(t, err)
	seedChannelRuntimeMeta(t, app, channelID, frame.ChannelTypePerson)

	require.NoError(t, app.Start())
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})

	recipientConn := connectAppWKProtoClient(t, app, "u2")
	sessionID := waitForPresenceSessionID(t, app, "u2")

	first, err := app.Message().Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "async-close-1",
		Payload:     []byte("hi close"),
	})
	require.NoError(t, err)

	recv := readAppRecvPacket(t, recipientConn)
	require.Equal(t, first.MessageSeq, recv.MessageSeq)
	require.Eventually(t, func() bool {
		return app.deliveryRuntime.HasAckBinding(sessionID, uint64(recv.MessageID))
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, recipientConn.Close())
	require.Eventually(t, func() bool {
		return !app.deliveryRuntime.HasAckBinding(sessionID, uint64(recv.MessageID))
	}, time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		routes, err := app.presenceApp.EndpointsByUID(context.Background(), "u2")
		return err == nil && len(routes) == 0
	}, time.Second, 10*time.Millisecond)

	second, err := app.Message().Send(context.Background(), message.SendCommand{
		FromUID:     "u1",
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: "async-close-2",
		Payload:     []byte("still durable"),
	})
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, second.Reason)
	require.NotZero(t, second.MessageSeq)
}

func TestAppStartServesLegacyUserTokenEndpoint(t *testing.T) {
	cfg := testConfig(t)
	cfg.API.ListenAddr = "127.0.0.1:0"

	app, err := New(cfg)
	require.NoError(t, err)

	require.NoError(t, app.Start())
	t.Cleanup(func() {
		require.NoError(t, app.Stop())
	})
	require.Eventually(t, func() bool {
		_, err := app.Cluster().LeaderOf(1)
		return err == nil
	}, 3*time.Second, 50*time.Millisecond)

	req, err := http.NewRequest(http.MethodPost, "http://"+app.API().Addr()+"/user/token", bytes.NewBufferString(`{"uid":"token-user","token":"token-1","device_flag":1,"device_level":1}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.JSONEq(t, `{"status":200}`, string(body))

	gotUser, err := app.Store().GetUser(context.Background(), "token-user")
	require.NoError(t, err)
	require.Equal(t, "token-user", gotUser.UID)

	gotDevice, err := app.Store().GetDevice(context.Background(), "token-user", 1)
	require.NoError(t, err)
	require.Equal(t, metadb.Device{
		UID:         "token-user",
		DeviceFlag:  1,
		Token:       "token-1",
		DeviceLevel: 1,
	}, gotDevice)
}
