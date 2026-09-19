package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// Delayed authoritative replies keep independent Slot work outstanding without
// introducing wall-clock sleeps or depending on the scheduler's speed.
type blockedMessageUpdateCluster struct {
	*promotedRPCRegistrationCluster
	release chan struct{}
	active  atomic.Int64
	peak    atomic.Int64
	calls   atomic.Int64
}

func (c *blockedMessageUpdateCluster) SlotForKey(key string) multiraft.SlotID {
	n, _ := strconv.Atoi(key)
	return multiraft.SlotID(n)
}

func (c *blockedMessageUpdateCluster) PeersForSlot(multiraft.SlotID) []multiraft.NodeID {
	return []multiraft.NodeID{2}
}

func (c *blockedMessageUpdateCluster) RPCService(ctx context.Context, _ multiraft.NodeID, slot multiraft.SlotID, service uint8, body []byte) ([]byte, error) {
	current := c.active.Add(1)
	defer c.active.Add(-1)
	c.calls.Add(1)
	for previous := c.peak.Load(); current > previous; previous = c.peak.Load() {
		if c.peak.CompareAndSwap(previous, current) {
			break
		}
	}
	select {
	case <-c.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var request messageUpdateReadRPC
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	if service != messageUpdateRPCServiceID || request.SlotID != uint64(slot) {
		return nil, errors.New("incorrect authoritative route")
	}
	pages := make([]metadb.MessageUpdatePage, len(request.Reads))
	for i, read := range request.Reads {
		pages[i].Head.ChannelID = read.ChannelID
		pages[i].Next = read.After
	}
	return json.Marshal(messageUpdateReadReply{Format: 1, Status: rpcStatusOK, Pages: pages})
}

func TestMessageUpdateReadOverlapsEightSlotsAndRetainsBounds(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(strconv.FormatBool(canceled), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c := &blockedMessageUpdateCluster{
					promotedRPCRegistrationCluster: &promotedRPCRegistrationCluster{leaderID: 2, localNodeID: 1},
					release:                        make(chan struct{}),
				}
				store := New(c, nil)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				// Duplicate each Slot with distinct cursors and reverse input order:
				// routing must preserve page alignment despite concurrent replies.
				reads := make([]metadb.MessageUpdateRead, 24)
				for i := range reads {
					reads[i] = metadb.MessageUpdateRead{ChannelID: strconv.Itoa(12 - i%12), ChannelType: 2, After: uint64(i + 1)}
				}
				var pages []metadb.MessageUpdatePage
				var err error
				go func() { pages, err = store.ReadMessageUpdatesBatch(ctx, reads) }()
				synctest.Wait()
				if got := c.active.Load(); got != 8 {
					t.Errorf("outstanding independent Slots = %d, want 8", got)
				}
				if canceled {
					cancel()
				} else {
					close(c.release)
				}
				synctest.Wait()
				if c.active.Load() != 0 || c.peak.Load() > 8 {
					t.Fatalf("workers leaked or exceeded bound: active=%d peak=%d", c.active.Load(), c.peak.Load())
				}
				if canceled {
					if !errors.Is(err, context.Canceled) || pages != nil {
						t.Fatalf("canceled read: pages=%v err=%v", pages, err)
					}
					return
				}
				if err != nil || len(pages) != len(reads) || c.calls.Load() != 12 {
					t.Fatalf("pages=%d calls=%d err=%v", len(pages), c.calls.Load(), err)
				}
				for i, page := range pages {
					if page.Head.ChannelID != reads[i].ChannelID || page.Next != reads[i].After {
						t.Fatalf("misaligned page %d: %+v", i, page)
					}
				}
			})
		})
	}
}
