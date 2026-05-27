package reactor

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/stretchr/testify/require"
)

func testMeta(id string, local ch.NodeID, leader ch.NodeID) ch.Meta {
	channelID := ch.ChannelID{ID: id, Type: 1}
	replicas := []ch.NodeID{leader}
	if local != leader {
		replicas = append(replicas, local)
	}
	return ch.Meta{
		Key:         ch.ChannelKeyForID(channelID),
		ID:          channelID,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      leader,
		Replicas:    replicas,
		ISR:         replicas,
		MinISR:      1,
		Status:      ch.StatusActive,
	}
}

func awaitSubmit(g *Group, key ch.ChannelKey, event Event) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	future, err := g.Submit(ctx, key, event)
	if err != nil {
		return err
	}
	_, err = future.Await(ctx)
	return err
}

func applyMetaDirect(t *testing.T, reactor *Reactor, meta ch.Meta) error {
	t.Helper()
	future := NewFuture()
	reactor.handleApplyMeta(Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta, Future: future})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := future.Await(ctx)
	return err
}

func awaitFutureResult(t *testing.T, future *Future) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := future.Await(ctx)
	require.NoError(t, err)
	return result
}

func seedCommittedMessage(t *testing.T, factory store.Factory, meta ch.Meta, id uint64, payload string) {
	t.Helper()
	cs, err := factory.ChannelStore(meta.Key, meta.ID)
	require.NoError(t, err)
	_, err = cs.AppendLeader(context.Background(), store.AppendLeaderRequest{Records: []ch.Record{{ID: id, Payload: []byte(payload), SizeBytes: len(payload)}}})
	require.NoError(t, err)
	require.NoError(t, cs.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: 1}))
}

func appendEvent(meta ch.Meta, id uint64, payload string) Event {
	return Event{
		Kind:   EventAppend,
		Key:    meta.Key,
		OpID:   ch.OpID(id),
		Future: NewFuture(),
		Append: ch.AppendBatchRequest{
			ChannelID:  meta.ID,
			CommitMode: ch.CommitModeLocal,
			Messages: []ch.Message{{
				MessageID:   id,
				ChannelID:   meta.ID.ID,
				ChannelType: meta.ID.Type,
				Payload:     []byte(payload),
			}},
		},
	}
}

func requireFuturePending(t *testing.T, future *Future) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := future.Await(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
