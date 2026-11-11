package message

import (
	"context"
	"errors"
	"fmt"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/stretchr/testify/require"
)

// Registration must publish real debt without waiting for other nodes or free
// capacity. Missing/uncertain proof, cancellation and competing ownership must
// stay closed, and a changed roster must invalidate an earlier registration.
func TestMQTTStorageRegistrationSeparatesDebtFromGrowth(t *testing.T) {
	for _, debt := range []uint64{0, 200} {
		t.Run(fmt.Sprint(debt), func(t *testing.T) {
			members := []uint64{1, 2, 3}
			calls := 0
			b := &mqttStorageBudget{used: debt, opts: MQTTStorageOptions{NodeBytes: 100, ClusterBytes: 100}}
			b.opts.Members = func() ([]uint64, uint64) { return members, 1 }
			b.opts.Adjust = func(_ context.Context, q MQTTStorageGrantRequest) (MQTTStorageGrantReply, error) {
				calls++
				status := "applied"
				if debt > 100 {
					status = "full"
				}
				return MQTTStorageGrantReply{Status: status, Revision: uint64(calls), Bytes: q.Target, Total: q.Target, Limit: 100, MembersMatch: true, Initialized: true, Debt: debt, RosterRevision: 1}, nil
			}
			e := &Engine{db: &MessageDB{mqttStorage: b}}
			require.NoError(t, e.RegisterMQTTStorage(context.Background()))
			require.NoError(t, e.RegisterMQTTStorage(context.Background()))
			require.Equal(t, 1, calls, "the same roster keeps its registration proof")
			require.Equal(t, debt, b.used)
			require.False(t, b.ready, "registration alone cannot grant growth")
			require.ErrorIs(t, b.reserve(context.Background(), 1), channel.ErrBackpressured)
			members = []uint64{1, 2, 3, 4}
			missing := errors.New("missing registration receipt")
			b.opts.Adjust = func(context.Context, MQTTStorageGrantRequest) (MQTTStorageGrantReply, error) {
				return MQTTStorageGrantReply{}, missing
			}
			require.ErrorIs(t, e.RegisterMQTTStorage(context.Background()), missing)
			require.False(t, b.registered)
		})
	}
}

func TestMQTTStorageRegistrationKeepsBoundedOwnership(t *testing.T) {
	b := &mqttStorageBudget{opts: MQTTStorageOptions{Members: func() ([]uint64, uint64) { t.Fatal("unadmitted registration"); return nil, 0 }}}
	e := &Engine{db: &MessageDB{mqttStorage: b}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, e.RegisterMQTTStorage(ctx), context.Canceled)
	b.mu.Lock()
	err := e.RegisterMQTTStorage(context.Background())
	b.mu.Unlock()
	require.ErrorIs(t, err, channel.ErrBackpressured)
}
