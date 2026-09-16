package channels

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/stretchr/testify/require"
)

type allocatedPageStore struct {
	channelstore.ChannelStore
	page func() []ch.Message
}

func (*allocatedPageStore) Load(context.Context) (channelstore.InitialState, error) {
	return channelstore.InitialState{LEO: 1, HW: 1}, nil
}
func (*allocatedPageStore) LoadRetentionState(context.Context) (channelstore.RetentionState, error) {
	return channelstore.RetentionState{}, nil
}
func (s *allocatedPageStore) ReadCommitted(context.Context, channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	if s.page != nil {
		return channelstore.ReadCommittedResult{Messages: s.page(), NextSeq: 2}, nil
	}
	return channelstore.ReadCommittedResult{Messages: []ch.Message{{MessageSeq: 1, Payload: []byte("owned")}}, NextSeq: 2}, nil
}
func (*allocatedPageStore) Close() error { return nil }

type allocatedPageFactory struct{ store *allocatedPageStore }

func (f allocatedPageFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (channelstore.ChannelStore, error) {
	return f.store, nil
}

func TestStoredReadOwnedPageAllocationBudget(t *testing.T) {
	ctx := context.Background()
	svc := &Service{store: allocatedPageFactory{store: &allocatedPageStore{}}}
	read := CommittedRead{ChannelID: ch.ChannelID{ID: "owned", Type: 2}, Request: channelstore.ReadCommittedRequest{FromSeq: 1, Limit: 1, MaxBytes: 1024}}
	allocs := testing.AllocsPerRun(100, func() {
		page, err := svc.readStoredMessages(ctx, read, 0, 1, 0, false, true)
		if err != nil || len(page.Messages) != 1 || string(page.Messages[0].Payload) != "owned" {
			t.Fatalf("invalid page: %+v %v", page, err)
		}
	})
	// The existing Channel key, storage message slice and storage payload require
	// three allocations. The service must not allocate a second page/payload copy.
	require.LessOrEqual(t, allocs, float64(3))
}

func TestRoutedReadResultsOwnIndependentMessages(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "remote"}[remote], func(t *testing.T) {
			ctx := context.Background()
			id := ch.ChannelID{ID: "read-ownership", Type: 2}
			factory := channelstore.NewMemoryFactory()
			store, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
			require.NoError(t, err)
			_, err = store.AppendLeader(ctx, channelstore.AppendLeaderRequest{Records: []ch.Record{{ID: 11, Payload: []byte("first")}, {ID: 12, Payload: []byte("second")}}})
			require.NoError(t, err)
			require.NoError(t, store.StoreCheckpoint(ctx, ch.Checkpoint{HW: 2}))
			require.NoError(t, store.Close())
			meta := ch.Meta{ID: id, Leader: 2, Epoch: 1, LeaderEpoch: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 1, Status: ch.StatusActive}
			network := clusternet.NewLocalNetwork()
			client := NewTransportClient(network)
			leader, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 2, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: factory, Forward: client})
			require.NoError(t, err)
			RegisterServiceHandlers(network, 2, leader)
			reader := leader
			if remote {
				reader, err = NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: factory, Forward: client})
				require.NoError(t, err)
			}
			for _, persisted := range []bool{false, true} {
				read := func() []CommittedReadResult {
					queries := []CommittedRead{{ChannelID: id, Request: channelstore.ReadCommittedRequest{FromSeq: 1, Limit: 2, MaxBytes: 1024}}}
					var pages []CommittedReadResult
					if persisted {
						pages, err = reader.ReadPersistedBatch(ctx, queries)
					} else {
						pages, err = reader.ReadCommittedBatch(ctx, queries)
					}
					require.NoError(t, err)
					require.Len(t, pages, 1)
					require.NoError(t, pages[0].Err)
					require.Len(t, pages[0].Read.Messages, 2)
					return pages
				}
				a, b := read(), read()
				a[0].Read.Messages[0].Payload[0] = 'X'
				a[0].Read.Messages[1].MessageID = 999
				require.Equal(t, "first", string(b[0].Read.Messages[0].Payload))
				require.Equal(t, uint64(12), b[0].Read.Messages[1].MessageID)
				require.Equal(t, "first", string(read()[0].Read.Messages[0].Payload))
			}
		})
	}
}

// Empty representations remain stable when owned pages replace deep copies.
func TestRoutedReadEmptyRepresentations(t *testing.T) {
	for _, remote := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			page func() []ch.Message
		}{
			{"nil-page", func() []ch.Message { return nil }},
			{"empty-page", func() []ch.Message { return []ch.Message{} }},
			{"nil-payload", func() []ch.Message { return []ch.Message{{MessageSeq: 1}} }},
			{"empty-payload", func() []ch.Message { return []ch.Message{{MessageSeq: 1, Payload: []byte{}}} }},
		} {
			t.Run(map[bool]string{false: "local", true: "remote"}[remote]+"/"+tc.name, func(t *testing.T) {
				id := ch.ChannelID{ID: "empty-representations", Type: 2}
				meta := ch.Meta{ID: id, Leader: 2, Epoch: 1, LeaderEpoch: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 1, Status: ch.StatusActive}
				network := clusternet.NewLocalNetwork()
				client := NewTransportClient(network)
				factory := allocatedPageFactory{store: &allocatedPageStore{page: tc.page}}
				leader, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 2, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: factory, Forward: client})
				require.NoError(t, err)
				RegisterServiceHandlers(network, 2, leader)
				reader := leader
				if remote {
					reader, err = NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: NewStaticMetaSource([]ch.Meta{meta}), Store: factory, Forward: client})
					require.NoError(t, err)
				}
				for _, persisted := range []bool{false, true} {
					queries := []CommittedRead{{ChannelID: id, Request: channelstore.ReadCommittedRequest{FromSeq: 1, Limit: 2, MaxBytes: 1024}}}
					var pages []CommittedReadResult
					if persisted {
						pages, err = reader.ReadPersistedBatch(context.Background(), queries)
					} else {
						pages, err = reader.ReadCommittedBatch(context.Background(), queries)
					}
					require.NoError(t, err)
					require.Len(t, pages, 1)
					require.NoError(t, pages[0].Err)
					require.NotNil(t, pages[0].Read.Messages)
					require.Len(t, pages[0].Read.Messages, len(tc.page()))
					for _, msg := range pages[0].Read.Messages {
						require.Nil(t, msg.Payload)
					}
				}
			})
		}
	}
}
