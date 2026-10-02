package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type mqttSourceNodeFixture struct {
	meta    meta.ChannelRuntimeMeta
	readErr error
	request ch.MQTTSourceRequest
	ensure  func(context.Context, ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error)
	read    func(context.Context, string, int64) (meta.ChannelRuntimeMeta, error)
	resolve func(context.Context, ch.ChannelID) (ch.Meta, error)
}

func (n *mqttSourceNodeFixture) GetChannelRuntimeMetaFresh(ctx context.Context, id string, kind int64) (meta.ChannelRuntimeMeta, error) {
	if n.read != nil {
		return n.read(ctx, id, kind)
	}
	return n.meta, n.readErr
}
func (n *mqttSourceNodeFixture) EnsureChannelMQTTSource(c context.Context, r ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
	n.request = r
	return n.ensure(c, r)
}

func (n *mqttSourceNodeFixture) ResolveChannelAppendAuthority(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if n.resolve != nil {
		return n.resolve(ctx, id)
	}
	return ch.Meta{}, errors.New("unexpected initialization")
}

type mqttSourceIDFixture uint64

func (n *mqttSourceIDFixture) Next() uint64 { *n++; return uint64(*n) }
func TestMQTTSourceAdapterPreservesFreshAuthority(t *testing.T) {
	for _, mode := range []string{"success", "read-error", "wrong-meta", "zero-route", "source-error", "invalid-source", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ids := mqttSourceIDFixture(100)
			node := &mqttSourceNodeFixture{meta: meta.ChannelRuntimeMeta{ChannelID: "group", ChannelType: 2, ChannelEpoch: 2, LeaderEpoch: 3, RouteGeneration: 4}, ensure: func(context.Context, ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
				return ch.MQTTSourceSnapshot{Generation: "protected", StartAfter: 1, CommittedThrough: 3}, nil
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "read-error":
				node.readErr = context.DeadlineExceeded
			case "wrong-meta":
				node.meta.ChannelID = "other"
			case "zero-route":
				node.meta.RouteGeneration = 0
			case "source-error":
				node.ensure = func(context.Context, ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
					return ch.MQTTSourceSnapshot{}, context.DeadlineExceeded
				}
			case "invalid-source":
				node.ensure = func(context.Context, ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
					return ch.MQTTSourceSnapshot{Generation: "protected", StartAfter: 3, CommittedThrough: 3}, nil
				}
			case "canceled":
				cancel()
			}
			adapter, err := NewMQTTSourceProtector(MQTTSourceProtectorOptions{Node: node, MessageIDs: &ids, Now: func() time.Time { return time.UnixMilli(1000) }})
			require.NoError(t, err)
			got, err := adapter.ProtectMQTTSource(ctx, sessioncase.SourceChannel{ID: "group", Type: 2})
			if mode != "success" {
				require.Error(t, err)
				require.Zero(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, sessioncase.SourceChannel{ID: "group", Type: 2}, got.Channel)
			require.Equal(t, uint64(1), got.ProtectedAfter)
			require.Equal(t, ch.MQTTSourceRequest{ChannelID: ch.ChannelID{ID: "group", Type: 2}, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4, MessageID: 101, ServerTimestampMS: 1000}, node.request)
		})
	}
}

func TestMQTTSourceAdapterInitializesOnlyConfirmedAbsenceThenRereads(t *testing.T) {
	for _, mode := range []string{"success", "unavailable", "cancel-first-read", "create-error", "cancel-create", "still-absent", "reread-error", "cancel-reread", "malformed-reread"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ids := mqttSourceIDFixture(100)
			var events []string
			unavailable := errors.New("authority unavailable")
			node := &mqttSourceNodeFixture{}
			reads := 0
			node.read = func(_ context.Context, id string, kind int64) (meta.ChannelRuntimeMeta, error) {
				events = append(events, "read")
				reads++
				require.Equal(t, "empty", id)
				require.EqualValues(t, 2, kind)
				if reads == 1 {
					if mode == "unavailable" {
						return meta.ChannelRuntimeMeta{}, unavailable
					}
					if mode == "cancel-first-read" {
						cancel()
					}
					return meta.ChannelRuntimeMeta{}, meta.ErrNotFound
				}
				if mode == "still-absent" {
					return meta.ChannelRuntimeMeta{}, meta.ErrNotFound
				}
				if mode == "reread-error" {
					return meta.ChannelRuntimeMeta{}, unavailable
				}
				if mode == "cancel-reread" {
					cancel()
				}
				m := meta.ChannelRuntimeMeta{ChannelID: "empty", ChannelType: 2, ChannelEpoch: 7, LeaderEpoch: 8, RouteGeneration: 9}
				if mode == "malformed-reread" {
					m.ChannelID = "wrong"
				}
				return m, nil
			}
			node.resolve = func(_ context.Context, id ch.ChannelID) (ch.Meta, error) {
				events = append(events, "initialize")
				require.Equal(t, ch.ChannelID{ID: "empty", Type: 2}, id)
				if mode == "create-error" {
					return ch.Meta{}, unavailable
				}
				if mode == "cancel-create" {
					cancel()
				}
				// A cached append route cannot authorize the protection request.
				return ch.Meta{ID: id, Epoch: 1, LeaderEpoch: 2, RouteGeneration: 3}, nil
			}
			node.ensure = func(_ context.Context, r ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
				events = append(events, "protect")
				require.Equal(t, ch.MQTTSourceRequest{ChannelID: ch.ChannelID{ID: "empty", Type: 2}, ExpectedChannelEpoch: 7, ExpectedLeaderEpoch: 8, ExpectedRouteGeneration: 9, MessageID: 101, ServerTimestampMS: 1000}, r)
				return ch.MQTTSourceSnapshot{Generation: "protected", StartAfter: 0, CommittedThrough: 1}, nil
			}
			protector, err := NewMQTTSourceProtector(MQTTSourceProtectorOptions{Node: node, MessageIDs: &ids, Now: func() time.Time { return time.UnixMilli(1000) }})
			require.NoError(t, err)
			got, err := protector.ProtectMQTTSource(ctx, sessioncase.SourceChannel{ID: "empty", Type: 2})
			expected := []string{"read", "initialize", "read"}
			switch mode {
			case "success":
				expected = append(expected, "protect")
			case "unavailable", "cancel-first-read":
				expected = expected[:1]
			case "create-error", "cancel-create":
				expected = expected[:2]
			}
			require.Equal(t, expected, events)
			if mode == "success" {
				require.NoError(t, err)
				require.Equal(t, sessioncase.ProtectedSource{Channel: sessioncase.SourceChannel{ID: "empty", Type: 2}, Generation: "protected", ProtectedAfter: 0, CommittedThrough: 1}, got)
			} else {
				require.Error(t, err)
				require.Zero(t, got)
				require.EqualValues(t, 100, ids)
				require.Zero(t, node.request)
			}
		})
	}
}
