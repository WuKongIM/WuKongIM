package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"strings"
	"testing"
	"time"
)

func TestMessageUpdateReadRoutesAndUsesFreshAppliedBarrier(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 8)
	key := findUIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, 2, 2, "edit")
	store := nodes[0].store
	probe := &editReadStageProbe{}
	nodes[1].store.messageUpdateObserver = probe
	if err := store.UpsertChannel(ctx, metadb.Channel{ChannelID: key, ChannelType: 2}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertChannelRuntimeMeta(ctx, metadb.ChannelRuntimeMeta{ChannelID: key, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, Leader: 2, MinISR: 1, Replicas: []uint64{2}, ISR: []uint64{2}}); err != nil {
		t.Fatal(err)
	}
	q := metadb.MessageUpdateMutation{Op: "init", ChannelID: key, ChannelType: 2, Generation: "g"}
	if out, err := store.ApplyMessageUpdate(ctx, q); err != nil || out.Status != "ok" {
		t.Fatalf("init=%+v %v", out, err)
	}
	q.Op = "update"
	q.MessageID = 100
	q.MessageSeq = 1
	q.ExpectedChannelEpoch = 1
	q.ExpectedRouteGeneration = 1
	q.RequestID = "r"
	q.Digest = strings.Repeat("a", 64)
	q.Payload = []byte("updated")
	if out, err := store.ApplyMessageUpdate(ctx, q); err != nil || out.Status != "ok" {
		t.Fatalf("edit=%+v %v", out, err)
	}
	for iteration := 0; iteration < 2; iteration++ {
		before := nodes[1].cluster.nextIndex[2]
		pages, err := store.ReadMessageUpdatesBatch(ctx, []metadb.MessageUpdateRead{{ChannelID: key, ChannelType: 2, IDs: []uint64{100}}, {ChannelID: key, ChannelType: 2}})
		if err != nil || len(pages) != 2 || len(pages[0].Updates) != 1 || string(pages[0].Updates[0].Payload) != "updated" {
			t.Fatalf("read=%+v %v", pages, err)
		}
		if nodes[1].cluster.nextIndex[2] != before+1 {
			t.Fatal("read batch did not establish exactly one fresh Slot barrier")
		}
	}
	if len(probe.events) != 4 {
		t.Fatalf("stages=%v", probe.events)
	}
	for i, e := range probe.events {
		want := "barrier/ok"
		if i%2 == 1 {
			want = "storage/ok"
		}
		if e != want {
			t.Fatalf("stages=%v", probe.events)
		}
	}
	// The origin has no replacement row; serving its convenient local DB would be stale.
	local, err := nodes[0].db.ForHashSlot(mustHashSlotForKey(t, nodes[0].cluster, key)).ReadMessageUpdates(ctx, metadb.MessageUpdateRead{ChannelID: key, ChannelType: 2, IDs: []uint64{100}})
	if err != nil || len(local.Updates) != 0 {
		t.Fatalf("unexpected local edit=%+v %v", local, err)
	}
	delete(nodes[1].cluster.handlers, messageUpdateRPCServiceID)
	before := nodes[1].cluster.nextIndex[2]
	q.ExpectedVersion = 1
	q.RequestID = "r2"
	if _, err := store.ApplyMessageUpdate(ctx, q); err == nil {
		t.Fatal("unsupported replica accepted edit")
	}
	if nodes[1].cluster.nextIndex[2] != before {
		t.Fatal("capability rejection still proposed command")
	}
}

func TestMessageUpdateReadRPCRejectsUnknownVersionsAndOversizedWork(t *testing.T) {
	store := New(&promotedRPCRegistrationCluster{}, nil)
	for _, request := range []messageUpdateReadRPC{{Format: 2, Probe: true}, {Format: 1, Reads: make([]metadb.MessageUpdateRead, metadb.MaxMessageUpdatePage+1)}} {
		body, _ := json.Marshal(request)
		if _, err := store.handleMessageUpdateReadRPC(context.Background(), body); err == nil {
			t.Fatal("invalid RPC accepted")
		}
	}
	body, _ := json.Marshal(messageUpdateReadRPC{Format: 1, Probe: true})
	raw, err := store.handleMessageUpdateReadRPC(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := decodeMessageUpdateReply(raw)
	if err != nil || reply.Status != rpcStatusOK {
		t.Fatalf("probe=%+v err=%v", reply, err)
	}
}

type changingReadAuthority struct {
	*proxyTestCluster
	changed bool
	mapping bool
}

func (c *changingReadAuthority) ReadSlotBarrier(context.Context, multiraft.SlotID) error {
	c.changed = true
	return nil
}
func (c *changingReadAuthority) HashSlotTableVersion() uint64 {
	v := c.proxyTestCluster.HashSlotTableVersion()
	if c.changed && !c.mapping {
		return v + 1
	}
	return v
}
func (c *changingReadAuthority) SlotForKey(key string) multiraft.SlotID {
	v := c.proxyTestCluster.SlotForKey(key)
	if c.changed && c.mapping {
		return v + 1
	}
	return v
}

func TestMessageUpdateReadAuthorityChangesRemainRetryable(t *testing.T) {
	for _, mapping := range []bool{false, true} {
		for _, rpc := range []bool{false, true} {
			t.Run(fmt.Sprintf("mapping=%t/rpc=%t", mapping, rpc), func(t *testing.T) {
				nodes := startTwoNodeHashSlotStores(t, 8)
				key := findUIDForSlot(t, nodes[1].cluster, 2, "edit-route")
				c := &changingReadAuthority{proxyTestCluster: nodes[1].cluster, mapping: mapping}
				probe := &editReadStageProbe{}
				s := NewChannelMetadataStore(c, nodes[1].db, probe)
				q := messageUpdateReadRPC{Format: 1, SlotID: 2, Reads: []metadb.MessageUpdateRead{{ChannelID: key, ChannelType: 2}}}
				var err error
				if rpc {
					b, _ := json.Marshal(q)
					_, err = s.handleMessageUpdateReadRPC(context.Background(), b)
				} else {
					_, err = s.readMessageUpdatesLocal(context.Background(), q)
				}
				if len(probe.events) != 2 || probe.events[0] != "barrier/ok" || probe.events[1] != "storage/error" {
					t.Fatalf("stages=%v", probe.events)
				}
				if !errors.Is(err, ErrReadStaleRoute) || errors.Is(err, metadb.ErrStaleMeta) {
					t.Fatalf("route read lost retry identity: %v", err)
				}
			})
		}
	}
}

type editReadStageProbe struct{ events []string }

func (p *editReadStageProbe) ObserveMessageUpdateReadStage(stage, result string, d time.Duration) {
	p.events = append(p.events, stage+"/"+result)
}

type canceledEditBarrier struct{ *proxyTestCluster }

func (c canceledEditBarrier) ReadSlotBarrier(ctx context.Context, _ multiraft.SlotID) error {
	return ctx.Err()
}
func TestMessageUpdateReadStagesStopAfterCanceledBarrier(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 8)
	key := findUIDForSlot(t, nodes[1].cluster, 2, "edit-cancel")
	probe := &editReadStageProbe{}
	store := NewChannelMetadataStore(canceledEditBarrier{nodes[1].cluster}, nodes[1].db, probe)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.readMessageUpdatesLocal(ctx, messageUpdateReadRPC{Format: 1, SlotID: 2, Reads: []metadb.MessageUpdateRead{{ChannelID: key, ChannelType: 2}}})
	if !errors.Is(err, context.Canceled) || len(probe.events) != 1 || probe.events[0] != "barrier/error" {
		t.Fatalf("err=%v stages=%v", err, probe.events)
	}
	disabled := &Store{}
	if !disabled.startMessageUpdateStage().IsZero() {
		t.Fatal("disabled observer reads clock")
	}
	if n := testing.AllocsPerRun(100, func() { disabled.finishMessageUpdateStage("barrier", disabled.startMessageUpdateStage(), nil) }); n != 0 {
		t.Fatalf("allocations=%v", n)
	}
}

func (p *editReadStageProbe) MessageUpdateReadObservationEnabled() bool { return true }
