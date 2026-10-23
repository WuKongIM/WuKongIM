package multiraft

import (
	"math"
	"sync"
	"unsafe"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	storageAppendPart uint8 = 1
	storageApplyPart  uint8 = 2
	storageSlotBytes  int64 = 8 << 20
	storageSlotOwners       = 4
)

type storageUsage struct {
	count int
	bytes int64
}

// storageBudget bounds newly owned storage work, including running work and
// completions awaiting the Raft owner. It does not bound RawNode memory or RSS.
type storageBudget struct {
	mu                   sync.Mutex
	slots                map[*slot]storageUsage
	used                 storageUsage
	nodeCount, slotCount int
	nodeBytes            int64
	// saves is shared by asynchronous writes, owner fallback and compaction.
	saves chan struct{}
}

// storageLease prepays at most two completion records for one accepted Ready.
// Its generation is the Slot pointer, never a reusable numeric Slot ID.
type storageLease struct {
	budget    *storageBudget
	slot      *slot
	bytes     int64
	remaining uint8
}

func newStorageBudget(workers, maxApplying int) (*storageBudget, error) {
	if workers <= 0 || workers > math.MaxInt/storageSlotOwners || int64(workers) > math.MaxInt64/storageSlotBytes {
		return nil, ErrInvalidOptions
	}
	count := storageSlotOwners
	if maxApplying > 0 && maxApplying < count {
		count = maxApplying
	}
	return &storageBudget{slots: make(map[*slot]storageUsage), nodeCount: workers * storageSlotOwners, slotCount: count, nodeBytes: int64(workers) * storageSlotBytes, saves: make(chan struct{}, workers)}, nil
}

// slotCountFull checks existing ownership before Ready accepts more work.
// Only this Slot's owner can add its leases; other goroutines only return them.
func (b *storageBudget) slotCountFull(g *slot) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.slots[g].count >= b.slotCount
}

func (b *storageBudget) reserve(g *slot, bytes int64, parts uint8) (*storageLease, bool) {
	return b.reserveWithReason(g, bytes, parts, nil)
}

// reserveWithReason snapshots diagnostic rejection bits under the existing
// admission lock. A nil output performs no extra classification.
func (b *storageBudget) reserveWithReason(g *slot, bytes int64, parts uint8, reject *uint8) (*storageLease, bool) {
	if b == nil || g == nil || bytes <= 0 || bytes > storageSlotBytes || parts == 0 || parts & ^uint8(storageAppendPart|storageApplyPart) != 0 {
		return nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	u := b.slots[g]
	if u.count >= b.slotCount || b.used.count >= b.nodeCount || bytes > storageSlotBytes-u.bytes || bytes > b.nodeBytes-b.used.bytes {
		if reject != nil {
			if u.count >= b.slotCount {
				*reject |= 1
			}
			if b.used.count >= b.nodeCount {
				*reject |= 2
			}
			if bytes > storageSlotBytes-u.bytes {
				*reject |= 4
			}
			if bytes > b.nodeBytes-b.used.bytes {
				*reject |= 8
			}
		}
		return nil, false
	}
	u.count++
	u.bytes += bytes
	b.slots[g] = u
	b.used.count++
	b.used.bytes += bytes
	return &storageLease{budget: b, slot: g, bytes: bytes, remaining: parts}, true
}

// complete refunds ownership only after every prepaid part has been consumed.
// The budget lock also makes duplicate completion and generation retirement safe.
func (l *storageLease) complete(part uint8) {
	if l == nil {
		return
	}
	b := l.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if l.remaining == 0 {
		return
	}
	l.remaining &^= part
	if l.remaining != 0 {
		return
	}
	u := b.slots[l.slot]
	u.count--
	u.bytes -= l.bytes
	if u.count == 0 {
		delete(b.slots, l.slot)
	} else {
		b.slots[l.slot] = u
	}
	b.used.count--
	b.used.bytes -= l.bytes
}
func (l *storageLease) retire() { l.complete(storageAppendPart | storageApplyPart) }

func storageChargeAdd(a, b int64) int64 {
	if a < 0 || b < 0 || b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}

func storageMessageCharge(m raftpb.Message) int64 {
	n := int64(unsafe.Sizeof(m))
	n = storageChargeAdd(n, int64(len(m.Context)))
	for _, e := range m.Entries {
		// Save constructs one additional compact Entry header slice; payload
		// bytes are shared by that slice and the owned message.
		n = storageChargeAdd(n, 2*int64(unsafe.Sizeof(e)))
		n = storageChargeAdd(n, int64(len(e.Data)))
	}
	for _, response := range m.Responses {
		n = storageChargeAdd(n, storageMessageCharge(response))
	}
	if m.Snapshot != nil {
		return math.MaxInt64
	} // snapshots always borrow synchronously
	return n
}

func storageReadyCharge(rd raft.Ready) int64 {
	// Fixed owner, two completion records, queue pointers and map occupancy.
	n := int64(2048)
	for _, m := range rd.Messages {
		if m.Type == raftpb.MsgStorageAppend || m.Type == raftpb.MsgStorageApply {
			n = storageChargeAdd(n, storageMessageCharge(m))
		}
	}
	for _, r := range rd.ReadStates {
		n = storageChargeAdd(n, int64(unsafe.Sizeof(r)))
		n = storageChargeAdd(n, int64(len(r.RequestCtx)))
	}
	return n
}

// cloneStorageMessage allocates exact-length slices after credits are reserved.
// Fallback never calls it, preserving the existing legal large-entry API.
func cloneStorageMessage(m raftpb.Message) raftpb.Message {
	c := m
	if len(m.Context) > 0 {
		c.Context = make([]byte, len(m.Context))
		copy(c.Context, m.Context)
	}
	if len(m.Entries) > 0 {
		c.Entries = make([]raftpb.Entry, len(m.Entries))
		for i, e := range m.Entries {
			c.Entries[i] = e
			if len(e.Data) > 0 {
				c.Entries[i].Data = make([]byte, len(e.Data))
				copy(c.Entries[i].Data, e.Data)
			}
		}
	}
	if len(m.Responses) > 0 {
		c.Responses = make([]raftpb.Message, len(m.Responses))
		for i, r := range m.Responses {
			c.Responses[i] = cloneStorageMessage(r)
		}
	}
	return c
}
