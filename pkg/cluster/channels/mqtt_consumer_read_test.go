package channels

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

type consumerReadStore struct {
	channelstore.ChannelStore
	page  ch.MQTTReplayPage
	calls int
}

func (s *consumerReadStore) ReadMQTTReplayAnchor(context.Context, uint64, ch.MQTTReplayRange) (ch.MQTTReplayPage, error) {
	s.calls++
	return s.page, nil
}
func (s *consumerReadStore) Close() error { return nil }

type consumerReadFactory struct {
	channelstore.Factory
	s *consumerReadStore
}

func (f consumerReadFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (channelstore.ChannelStore, error) {
	return f.s, nil
}

func TestMQTTConsumerReadRequiresStableAuthorityAndBoundedServing(t *testing.T) {
	for _, mode := range []string{"success", "route_changed", "fence_changed", "wrong_server", "backpressure", "bad_page", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			_, m, r, q, page := mqttRoutedReplayFixture(t)
			st := &consumerReadStore{page: page}
			s, e := NewService(Config{LocalNode: 2, MetaSource: m, Runtime: r, Store: consumerReadFactory{s: st}})
			require.NoError(t, e)
			request := ch.MQTTReplayConsumerRequest{Request: q, AnchorPosition: 5}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m.after = func(n int) {
				if n != 2 {
					return
				}
				if mode == "route_changed" {
					m.meta.RouteGeneration++
				}
				if mode == "fence_changed" {
					m.meta.WriteFence = ch.WriteFence{Token: "changed", Version: 1}
				}
			}
			switch mode {
			case "backpressure":
				for range cap(s.mqttConsumerReads) {
					s.mqttConsumerReads <- struct{}{}
				}
			case "bad_page":
				st.page.After.Generation = "foreign"
			case "canceled":
				cancel()
			}
			var got ch.MQTTReplayPage
			if mode == "wrong_server" {
				got, e = s.handleForwardMQTTConsumerRead(ctx, mqttConsumerReadForwardRequest{Leader: 3, Request: request})
			} else {
				got, e = s.ReadMQTTReplay(ctx, request)
			}
			if mode == "success" {
				require.NoError(t, e)
				require.Equal(t, page, got)
			} else {
				require.Error(t, e)
				require.Zero(t, got)
			}
			if mode == "backpressure" || mode == "canceled" || mode == "wrong_server" {
				require.Zero(t, st.calls)
			}
		})
	}
}

func TestMQTTConsumerReadRPCBindsAnchorAndBoundedPage(t *testing.T) {
	_, _, _, base, page := mqttRoutedReplayFixture(t)
	q := mqttConsumerReadForwardRequest{Leader: 2, Request: ch.MQTTReplayConsumerRequest{Request: base, AnchorPosition: 5}}
	encoded, e := encodeMQTTConsumerReadRequest(q)
	require.NoError(t, e)
	actual, e := decodeMQTTConsumerReadRequest(encoded)
	require.NoError(t, e)
	require.Equal(t, q, actual)
	reply, e := encodeMQTTConsumerReadReply(q, page, nil)
	require.NoError(t, e)
	decoded, e := decodeMQTTConsumerReadReply(reply, q)
	require.NoError(t, e)
	require.Equal(t, page, decoded)
	wrong := q
	wrong.Request.AnchorPosition++
	_, e = decodeMQTTConsumerReadReply(reply, wrong)
	require.Error(t, e)
	for i := range encoded {
		_, e = decodeMQTTConsumerReadRequest(encoded[:i])
		require.Error(t, e)
	}
	_, e = decodeMQTTConsumerReadRequest(append(encoded, 0))
	require.Error(t, e)
	for _, n := range []int{0, 1, 8, len(reply) - 1} {
		_, e = decodeMQTTConsumerReadReply(reply[:n], q)
		require.Error(t, e)
	}
	_, e = decodeMQTTConsumerReadReply(append(reply, 0), q)
	require.Error(t, e)
}
