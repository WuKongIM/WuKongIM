package cluster

import (
	"context"
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
}

func (n *mqttSourceNodeFixture) GetChannelRuntimeMetaFresh(context.Context, string, int64) (meta.ChannelRuntimeMeta, error) {
	return n.meta, n.readErr
}
func (n *mqttSourceNodeFixture) EnsureChannelMQTTSource(c context.Context, r ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
	n.request = r
	return n.ensure(c, r)
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
