package worker

import (
	"context"
	"strings"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

type willReceiptWorkerStore struct {
	mqttSourceWorkerStore
	receipt  ch.WillReceipt
	found    bool
	uid, key string
}

func (s *willReceiptWorkerStore) LookupWillReceipt(_ context.Context, uid, key string) (ch.WillReceipt, bool, error) {
	s.calls = append(s.calls, "receipt")
	s.uid, s.key = uid, key
	if s.panicRead {
		panic("receipt")
	}
	return s.receipt, s.found, s.readErr
}

type willReceiptWorkerFactory struct{ s store.ChannelStore }

func (f willReceiptWorkerFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (store.ChannelStore, error) {
	return f.s, nil
}

func TestWillReceiptWorkerPinsHWAndClosesLease(t *testing.T) {
	for _, mode := range []string{"success", "absent", "checkpoint", "read", "panic", "invalid", "above_hw", "partial_absent", "unsupported", "wrong_key", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			s := &willReceiptWorkerStore{found: true, receipt: ch.WillReceipt{MessageID: 9, MessageSeq: 2, ServerTimestampMS: 1000, ContentHash: [32]byte{1}}}
			q := ch.WillReceiptRequest{ChannelID: ch.ChannelID{ID: "will", Type: 2}, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 2, ExpectedRouteGeneration: 3, FromUID: "sender", ServerWillKey: "mqtt-will-v1:" + strings.Repeat("a", 64)}
			task := Task{Kind: TaskStoreWillReceipt, Fence: ch.Fence{ChannelKey: ch.ChannelKeyForID(q.ChannelID), OpID: 7}, StoreWillReceipt: &StoreWillReceiptTask{Request: q, CommittedThrough: 3}}
			var lease store.ChannelStore = s
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "absent":
				s.found = false
				s.receipt = ch.WillReceipt{}
			case "checkpoint":
				s.checkpointErr = ch.ErrNotReady
			case "read":
				s.readErr = ch.ErrLogConflict
			case "panic":
				s.panicRead = true
			case "invalid":
				s.receipt.ContentHash = [32]byte{}
			case "above_hw":
				s.receipt.MessageSeq = 4
			case "partial_absent":
				s.found = false
			case "unsupported":
				lease = &s.mqttSourceWorkerStore
			case "wrong_key":
				task.Fence.ChannelKey = "2:wrong"
			case "canceled":
				cancel()
			}
			run := func() {
				r := task.Run(ctx, Deps{Stores: willReceiptWorkerFactory{lease}})
				require.Equal(t, task.Fence, r.Fence)
				if mode == "success" || mode == "absent" {
					require.NoError(t, r.Err)
					require.NotNil(t, r.StoreWillReceipt)
					require.Equal(t, ch.WillReceiptResult{CommittedThrough: 3, Found: s.found, Receipt: s.receipt}, r.StoreWillReceipt.Result)
					require.Equal(t, q.FromUID, s.uid)
					require.Equal(t, q.ServerWillKey, s.key)
				} else {
					require.Error(t, r.Err)
					require.Nil(t, r.StoreWillReceipt)
				}
			}
			if mode == "panic" {
				require.Panics(t, run)
			} else {
				run()
			}
			if mode == "wrong_key" || mode == "canceled" {
				require.Empty(t, s.calls)
			} else {
				require.Equal(t, "close", s.calls[len(s.calls)-1])
				if mode == "unsupported" {
					require.Equal(t, []string{"close"}, s.calls)
				} else {
					require.EqualValues(t, 3, s.through)
				}
			}
		})
	}
	p := &Pools{StoreCheckpoint: &Pool{}, StoreRead: &Pool{}}
	require.Same(t, p.StoreCheckpoint, p.poolFor(TaskStoreWillReceipt))
}
