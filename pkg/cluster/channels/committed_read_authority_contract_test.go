package channels

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

func TestForwardCommittedReadsPreservesAlignedAuthorityFailuresAndLaggingMetaFallback(t *testing.T) {
	local := ch.ChannelID{ID: "local", Type: 2}
	remote := ch.ChannelID{ID: "remote", Type: 2}
	stale := ch.ChannelID{ID: "stale", Type: 2}
	deleting := ch.ChannelID{ID: "deleting", Type: 2}
	fallback := ch.ChannelID{ID: "fallback", Type: 2}
	missing := ch.ChannelID{ID: "missing", Type: 2}
	factory := channelstore.NewMemoryFactory()
	for _, item := range []struct {
		id      ch.ChannelID
		payload string
	}{
		{id: local, payload: "local-message"},
		{id: fallback, payload: "fallback-message"},
	} {
		store, err := factory.ChannelStore(ch.ChannelKeyForID(item.id), item.id)
		if err != nil {
			t.Fatalf("ChannelStore(%v) error = %v", item.id, err)
		}
		if _, err := store.AppendLeader(context.Background(), channelstore.AppendLeaderRequest{Records: []ch.Record{{ID: 1, Payload: []byte(item.payload)}}}); err != nil {
			t.Fatalf("AppendLeader(%v) error = %v", item.id, err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("Close(%v) error = %v", item.id, err)
		}
	}
	source := NewStaticMetaSource([]ch.Meta{
		{ID: local, Epoch: 2, LeaderEpoch: 3, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive},
		{ID: remote, Epoch: 2, LeaderEpoch: 3, Leader: 2, Replicas: []ch.NodeID{2}, ISR: []ch.NodeID{2}, MinISR: 1, Status: ch.StatusActive},
		{ID: stale, Epoch: 2, LeaderEpoch: 3, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive},
		{ID: deleting, Epoch: 2, LeaderEpoch: 3, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusDeleting},
	})
	service, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: source, Store: factory})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	gateway := NewServiceGateway(service)
	request := CommittedReadsRequest{Items: []CommittedReadRequest{
		{CommittedRead: committedReadContract(local), ExpectedLeader: 1, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedMinISR: 1},
		{CommittedRead: committedReadContract(remote), ExpectedLeader: 2, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedMinISR: 1},
		{CommittedRead: committedReadContract(stale), ExpectedLeader: 1, ExpectedChannelEpoch: 3, ExpectedLeaderEpoch: 3, ExpectedMinISR: 1},
		{CommittedRead: committedReadContract(deleting), ExpectedLeader: 1, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedMinISR: 1},
		{CommittedRead: committedReadContract(fallback), ExpectedLeader: 1, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedMinISR: 1},
		{CommittedRead: committedReadContract(missing)},
	}}

	response, err := gateway.handleForwardCommittedReads(context.Background(), request)
	if err != nil {
		t.Fatalf("handleForwardCommittedReads() error = %v", err)
	}
	if len(response.Items) != len(request.Items) {
		t.Fatalf("response items = %d, want %d", len(response.Items), len(request.Items))
	}
	if got := response.Items[0]; got.Err != nil || len(got.Read.Messages) != 1 || string(got.Read.Messages[0].Payload) != "local-message" {
		t.Fatalf("local result = %#v", got)
	}
	if !errors.Is(response.Items[1].Err, ch.ErrNotLeader) {
		t.Fatalf("remote result error = %v, want ErrNotLeader", response.Items[1].Err)
	}
	if !errors.Is(response.Items[2].Err, ch.ErrStaleMeta) {
		t.Fatalf("stale result error = %v, want ErrStaleMeta", response.Items[2].Err)
	}
	if !errors.Is(response.Items[3].Err, ch.ErrChannelNotFound) {
		t.Fatalf("deleting result error = %v, want ErrChannelNotFound", response.Items[3].Err)
	}
	if got := response.Items[4]; got.Err != nil || len(got.Read.Messages) != 1 || string(got.Read.Messages[0].Payload) != "fallback-message" {
		t.Fatalf("lagging metadata fallback result = %#v", got)
	}
	if !errors.Is(response.Items[5].Err, ch.ErrChannelNotFound) {
		t.Fatalf("unfenced missing result error = %v, want ErrChannelNotFound", response.Items[5].Err)
	}
}

func committedReadContract(id ch.ChannelID) CommittedRead {
	return CommittedRead{
		ChannelID: id,
		Request: channelstore.ReadCommittedRequest{
			FromSeq:  1,
			MaxSeq:   10,
			Limit:    10,
			MaxBytes: 4096,
		},
	}
}
