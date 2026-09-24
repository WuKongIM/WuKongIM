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
	page  ch.MQTTReplayConsumerPage
	calls int
}

func (s *consumerReadStore) ReadMQTTReplayAnchor(context.Context, uint64, ch.MQTTReplayRange) (ch.MQTTReplayConsumerPage, error) {
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
			_, m, r, q, _ := mqttRoutedReplayFixture(t)
			page := typedConsumerFixture(t, q)
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
			var got ch.MQTTReplayConsumerPage
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
	_, _, _, base, _ := mqttRoutedReplayFixture(t)
	page := typedConsumerFixture(t, base)
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

func typedConsumerFixture(t *testing.T, q ch.MQTTReplayRequest) ch.MQTTReplayConsumerPage {
	t.Helper()
	msg := publicationCodecMessage(t)
	msg.ChannelID, msg.ChannelType, msg.MessageSeq = q.ChannelID.ID, q.ChannelID.Type, 1
	msg.TraceID, msg.ChannelKey, msg.Version, msg.UpdatedAtMS = "", "", 0, 0
	size := uint64(len(msg.Payload) + len(msg.PublicationMetadata))
	prefix := ch.MQTTReplayPrefix{Generation: q.Range.Generation, Through: 1, TotalBytes: size, TotalStoredBytes: size + 1024, Digest: [32]byte{1}}
	return ch.MQTTReplayConsumerPage{Before: ch.MQTTReplayPrefix{Generation: q.Range.Generation}, After: prefix, Records: []ch.MQTTReplayPublication{{Message: msg, ContentVersion: 1, ContentHash: [32]byte{2}, Digest: prefix.Digest, AccountedBytes: size, TotalBytes: size, TotalStoredBytes: prefix.TotalStoredBytes}}}
}

func TestMQTTConsumerTypedRPCRejectsInvalidContentAndOldEnvelope(t *testing.T) {
	_, _, _, base, _ := mqttRoutedReplayFixture(t)
	q := mqttConsumerReadForwardRequest{Leader: 2, Request: ch.MQTTReplayConsumerRequest{Request: base, AnchorPosition: 5}}
	for _, fault := range []string{"channel", "position", "accounted", "stored", "metadata", "timestamp", "control", "overlay"} {
		t.Run(fault, func(t *testing.T) {
			p := typedConsumerFixture(t, base)
			switch fault {
			case "channel":
				p.Records[0].Message.ChannelID = "foreign"
			case "position":
				p.Records[0].Message.MessageSeq++
			case "accounted":
				p.Records[0].AccountedBytes++
			case "stored":
				p.Records[0].TotalStoredBytes = 0
			case "metadata":
				p.Records[0].Message.PublicationMetadata[0] = 255
			case "timestamp":
				p.Records[0].Message.ServerTimestampMS = 0
			case "control":
				p.Records[0].Internal = true
				p.Records[0].Message.SyncOnce = false
			case "overlay":
				p.Records[0].Message.Version = 2
			}
			_, err := encodeMQTTConsumerReadReply(q, p, nil)
			require.Error(t, err)
		})
	}
	p := typedConsumerFixture(t, base)
	encoded, err := encodeMQTTConsumerReadReply(q, p, nil)
	require.NoError(t, err)
	for i := range encoded {
		_, err = decodeMQTTConsumerReadReply(encoded[:i], q)
		require.Error(t, err)
	}
	decoded, err := decodeMQTTConsumerReadReply(encoded, q)
	require.NoError(t, err)
	clear(encoded)
	require.Equal(t, p, decoded)
	request, err := encodeMQTTConsumerReadRequest(q)
	require.NoError(t, err)
	request[4] = 1
	_, err = decodeMQTTConsumerReadRequest(request)
	require.Error(t, err)
	reply, err := encodeMQTTConsumerReadReply(q, p, nil)
	require.NoError(t, err)
	reply[4] = 1
	_, err = decodeMQTTConsumerReadReply(reply, q)
	require.Error(t, err)
	reply, err = encodeMQTTConsumerReadReply(q, ch.MQTTReplayConsumerPage{}, ch.ErrBackpressured)
	require.NoError(t, err)
	got, err := decodeMQTTConsumerReadReply(reply, q)
	require.ErrorIs(t, err, ch.ErrBackpressured)
	require.Zero(t, got)
}
