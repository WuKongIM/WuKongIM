package channels

import (
	"context"
	"errors"
	"reflect"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

func TestQuorumPeerLinkRoundTripsOneBoundedBatch(t *testing.T) {
	t.Parallel()

	network := clusternet.NewLocalNetwork()
	server := &captureQuorumExchangeServer{}
	RegisterQuorumExchangeHandlerOn(localNetworkRegistrar{network: network, nodeID: 2}, server)
	link, err := NewQuorumPeerLink(1, network)
	if err != nil {
		t.Fatalf("NewQuorumPeerLink() error = %v", err)
	}
	batch := replication.ExchangeBatch{Version: replication.ExchangeVersion, Items: []replication.ExchangeItem{{
		RequestID: 7,
		Kind:      replication.ExchangeProbe,
		Probe: &replication.ProbeRequest{
			ChannelKey: "1:rpc", ChannelID: ch.ChannelID{ID: "rpc", Type: 1}, Leader: 1, Follower: 2,
		},
	}}}
	want := replication.ExchangeBatchResult{Version: replication.ExchangeVersion, Items: []replication.ExchangeItemResult{{RequestID: 7}}}
	server.result = want

	got, err := link.Exchange(context.Background(), 2, batch)
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Exchange() = %#v, want %#v", got, want)
	}
	if server.from != 1 || !reflect.DeepEqual(server.batch, batch) {
		t.Fatalf("server received from=%d batch=%#v, want from=1 batch=%#v", server.from, server.batch, batch)
	}
}

func TestQuorumPeerLinkRejectsNilContextBeforeTransport(t *testing.T) {
	t.Parallel()

	link, err := NewQuorumPeerLink(1, clusternet.NewLocalNetwork())
	if err != nil {
		t.Fatalf("NewQuorumPeerLink() error = %v", err)
	}
	_, err = link.Exchange(nil, 2, replication.ExchangeBatch{Version: replication.ExchangeVersion, Items: []replication.ExchangeItem{{
		RequestID: 1,
		Kind:      replication.ExchangeProbe,
		Probe: &replication.ProbeRequest{
			ChannelKey: "1:nil-context", ChannelID: ch.ChannelID{ID: "nil-context", Type: 1}, Leader: 1, Follower: 2,
		},
	}}})
	if !errors.Is(err, ch.ErrInvalidConfig) {
		t.Fatalf("Exchange(nil context) error = %v, want ErrInvalidConfig", err)
	}
}

type captureQuorumExchangeServer struct {
	from   ch.NodeID
	batch  replication.ExchangeBatch
	result replication.ExchangeBatchResult
}

func (s *captureQuorumExchangeServer) Handle(_ context.Context, from ch.NodeID, batch replication.ExchangeBatch) (replication.ExchangeBatchResult, error) {
	s.from = from
	s.batch = batch
	return s.result, nil
}
