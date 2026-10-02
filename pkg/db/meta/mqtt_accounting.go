package meta

import (
	"encoding/binary"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
)

// MQTTAccountingItem is one originally charged source position. It stores no
// body or exchange identity; later policy changes cannot change its debit.
type MQTTAccountingItem struct {
	Position uint64 `json:"position"`
	Bytes    uint64 `json:"bytes"`
}

// MQTTQualifiedAccounting supplies a bounded, caller-proved qualification page.
// The parent mutation supplies Through and totals. SubscriptionRevision fences
// option replacement between proof capture and commit; storage does not prove
// current receive permission, original content, or the qualification policy.
type MQTTQualifiedAccounting struct {
	From                 uint64               `json:"from"`
	SubscriptionRevision uint64               `json:"subscription_revision"`
	Items                []MQTTAccountingItem `json:"items"`
}

// MQTTAccountingRange is a cursor-owned auxiliary System-1 receipt. Positive
// pages form a forward-only chain; omitted ranges are zero-charge coverage.
// Each receipt spans at most 256 positions, regardless of its qualifying count.
type MQTTAccountingRange struct {
	Key                  MQTTDeliveryCursorKey `json:"key"`
	From                 uint64                `json:"from"`
	Through              uint64                `json:"through"`
	SubscriptionRevision uint64                `json:"subscription_revision"`
	EvaluatedAtMS        int64                 `json:"evaluated_at_ms"`
	NextFrom             uint64                `json:"next_from"`
	Items                []MQTTAccountingItem  `json:"items"`
}

func accountingTotals(from, through uint64, items []MQTTAccountingItem) (uint64, bool) {
	if from == 0 || through < from || through-from >= 256 || len(items) > 256 {
		return 0, false
	}
	var total uint64
	previous := from - 1
	for _, v := range items {
		if v.Position <= previous || v.Position > through || math.MaxUint64-total < v.Bytes {
			return 0, false
		}
		previous = v.Position
		total += v.Bytes
	}
	return total, true
}

// ValidateMQTTAccountingRange validates format and bounds, not source evidence.
func ValidateMQTTAccountingRange(r MQTTAccountingRange) error {
	if validateMQTTDeliveryCursorKey(r.Key) != nil || r.SubscriptionRevision == 0 || r.EvaluatedAtMS <= 0 || len(r.Items) == 0 || (r.NextFrom != 0 && r.NextFrom <= r.Through) {
		return dberrors.ErrInvalidArgument
	}
	if _, ok := accountingTotals(r.From, r.Through, r.Items); !ok {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// ValidateMQTTAccountingHead checks the exact head witness against its cursor.
// It never treats missing or malformed auxiliary state as empty backlog.
func ValidateMQTTAccountingHead(c MQTTDeliveryCursor, r *MQTTAccountingRange) error {
	bad := dberrors.ErrCorruptValue
	if ValidateMQTTDeliveryCursor(c) != nil {
		return bad
	}
	if c.AccountingHead == 0 {
		if r != nil {
			return bad
		}
		return nil
	}
	if r == nil || ValidateMQTTAccountingRange(*r) != nil || r.Key != c.Key || r.From != c.AccountingHead || r.Through > c.AccountedThrough || r.SubscriptionRevision > c.Revision || r.EvaluatedAtMS > c.UpdatedAtMS ||
		(r.NextFrom == 0) != (c.AccountingHead == c.AccountingTail) || r.NextFrom > c.AccountingTail {
		return bad
	}
	var count, total uint64
	for _, item := range r.Items {
		if item.Position > c.WindowThrough {
			count++
			total += item.Bytes
		}
	}
	if count == 0 || count > c.PendingMessages-uint64(c.InflightCount) || total > c.PendingBytes-c.InflightBytes {
		return bad
	}
	if r.NextFrom == 0 && (count != c.PendingMessages-uint64(c.InflightCount) || total != c.PendingBytes-c.InflightBytes) {
		return bad
	}
	return nil
}

// mqttAccountingKey belongs to the cursor's existing table, distinct from rows
// and indexes. Hash-Slot snapshots already preserve this System namespace.
func mqttAccountingKey(slot HashSlot, c MQTTDeliveryCursorKey, from uint64) ([]byte, error) {
	if validateMQTTDeliveryCursorKey(c) != nil || from == 0 {
		return nil, dberrors.ErrInvalidArgument
	}
	var builder keycodec.Builder
	key := builder.Reset().Domain(keycodec.DomainMeta).Partition(keycodec.PartitionHashSlot, hashSlotPartitionID(slot)).System(TableIDMQTTDeliveryCursor, 1).Key()
	key, err := encodeKeyParts(key, mqttDeliveryCursorPrimaryKey(c))
	if err != nil {
		return nil, err
	}
	return binary.BigEndian.AppendUint64(key, from), nil
}
func encodeMQTTAccountingRange(key []byte, r MQTTAccountingRange) ([]byte, error) {
	if err := ValidateMQTTAccountingRange(r); err != nil {
		return nil, err
	}
	payload := make([]byte, 0, 42+16*len(r.Items))
	for _, n := range []uint64{r.From, r.Through, r.SubscriptionRevision, uint64(r.EvaluatedAtMS), r.NextFrom} {
		payload = binary.BigEndian.AppendUint64(payload, n)
	}
	payload = binary.BigEndian.AppendUint16(payload, uint16(len(r.Items)))
	for _, item := range r.Items {
		payload = binary.BigEndian.AppendUint64(payload, item.Position)
		payload = binary.BigEndian.AppendUint64(payload, item.Bytes)
	}
	return rowcodec.Wrap(key, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, payload), nil
}
func decodeMQTTAccountingRange(key []byte, c MQTTDeliveryCursorKey, from uint64, value []byte) (MQTTAccountingRange, error) {
	var r MQTTAccountingRange
	if len(value) > 4200 {
		return r, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return r, err
	}
	p := env.Payload
	if env.Version != 1 || env.Codec != rowcodec.CodecFixed || env.Flags != rowcodec.FlagChecksum || len(p) < 42 {
		return r, dberrors.ErrCorruptValue
	}
	count := int(binary.BigEndian.Uint16(p[40:42]))
	if count == 0 || count > 256 || len(p) != 42+16*count {
		return r, dberrors.ErrCorruptValue
	}
	r = MQTTAccountingRange{Key: c, From: binary.BigEndian.Uint64(p), Through: binary.BigEndian.Uint64(p[8:]), SubscriptionRevision: binary.BigEndian.Uint64(p[16:]), EvaluatedAtMS: int64(binary.BigEndian.Uint64(p[24:])), NextFrom: binary.BigEndian.Uint64(p[32:]), Items: make([]MQTTAccountingItem, count)}
	for i := range r.Items {
		r.Items[i] = MQTTAccountingItem{Position: binary.BigEndian.Uint64(p[42+i*16:]), Bytes: binary.BigEndian.Uint64(p[50+i*16:])}
	}
	if r.From != from || ValidateMQTTAccountingRange(r) != nil {
		return MQTTAccountingRange{}, dberrors.ErrCorruptValue
	}
	return r, nil
}
func loadMQTTAccounting(state *batchCommitState, slot HashSlot, c MQTTDeliveryCursorKey, from uint64) (MQTTAccountingRange, error) {
	key, err := mqttAccountingKey(slot, c, from)
	if err != nil {
		return MQTTAccountingRange{}, err
	}
	value, found, err := mqttDeliveryCursorTable.loadBatchValue(state, key)
	if err != nil {
		return MQTTAccountingRange{}, err
	}
	if !found {
		return MQTTAccountingRange{}, dberrors.ErrCorruptValue
	}
	return decodeMQTTAccountingRange(key, c, from, value)
}
func stageMQTTAccounting(state *batchCommitState, b *engine.Batch, slot HashSlot, r MQTTAccountingRange, erase bool) error {
	key, err := mqttAccountingKey(slot, r.Key, r.From)
	if err != nil {
		return err
	}
	var value []byte
	if erase {
		err = b.Delete(key)
	} else {
		value, err = encodeMQTTAccountingRange(key, r)
		if err == nil {
			err = b.Set(key, value)
		}
	}
	if err != nil {
		return err
	}
	state.tableRows[string(key)] = tableRowOverlay{value: value, exists: !erase}
	return nil
}

// prepareMQTTAccountingAppend changes only candidate state. Its bounded writes
// are staged after all conditional checks, so a conflict cannot mutate a tail.
func prepareMQTTAccountingAppend(state *batchCommitState, slot HashSlot, c *MQTTDeliveryCursor, m MQTTDeliveryCursorMutation) ([]MQTTAccountingRange, bool, error) {
	q := m.Qualified
	if c.AccountingVersion == 0 && c.WindowThrough != c.AccountedThrough || q.From != c.AccountedThrough+1 {
		return nil, false, nil
	}
	c.AccountingVersion = 1
	if len(q.Items) == 0 {
		return nil, true, nil
	}
	r := MQTTAccountingRange{Key: c.Key, From: q.From, Through: m.Through, SubscriptionRevision: q.SubscriptionRevision, EvaluatedAtMS: m.UpdatedAtMS, Items: q.Items}
	changes := make([]MQTTAccountingRange, 0, 2)
	if c.AccountingTail != 0 {
		tail, err := loadMQTTAccounting(state, slot, c.Key, c.AccountingTail)
		if err != nil {
			return nil, false, err
		}
		if tail.NextFrom != 0 || tail.Through >= r.From || tail.Through > c.AccountedThrough {
			return nil, false, dberrors.ErrCorruptValue
		}
		tail.NextFrom = r.From
		changes = append(changes, tail)
	} else {
		c.AccountingHead = r.From
	}
	c.AccountingTail = r.From
	return append(changes, r), true, nil
}

// prepareMQTTAccountingConsume visits at most the head and its next witness.
// Admission moves exactly one charge into inflight; Advance debits the selected
// head prefix. No changed subscription option can reclassify these old charges.
func prepareMQTTAccountingConsume(state *batchCommitState, slot HashSlot, c *MQTTDeliveryCursor, m MQTTWindowMutation) (*MQTTAccountingRange, bool, error) {
	if c.AccountingHead == 0 {
		return nil, m.Op == MQTTWindowAdvance && m.ReleasedMessages == 0 && m.ReleasedBytes == 0, nil
	}
	r, err := loadMQTTAccounting(state, slot, c.Key, c.AccountingHead)
	if err != nil {
		return nil, false, err
	}
	if err = ValidateMQTTAccountingHead(*c, &r); err != nil {
		return nil, false, err
	}
	through := m.Through
	if m.Op == MQTTWindowAdmit {
		through = m.Publication.Position
	}
	if r.NextFrom != 0 && through >= r.NextFrom {
		return nil, false, nil
	}
	var count, total uint64
	for _, item := range r.Items {
		if item.Position <= c.WindowThrough || item.Position > through {
			continue
		}
		count++
		total += item.Bytes
	}
	if m.Op == MQTTWindowAdmit {
		if count != 1 || total != m.Publication.Bytes {
			return nil, false, nil
		}
		exact := false
		for _, item := range r.Items {
			if item.Position == through {
				exact = true
				break
			}
		}
		if !exact {
			return nil, false, nil
		}
	} else if count != m.ReleasedMessages || total != m.ReleasedBytes {
		return nil, false, nil
	}
	if through < r.Items[len(r.Items)-1].Position {
		return nil, true, nil
	}
	if r.NextFrom != 0 {
		next, err := loadMQTTAccounting(state, slot, c.Key, r.NextFrom)
		if err != nil {
			return nil, false, err
		}
		// Model the consumed charges as released solely for validating the next
		// unadmitted head. Actual admission keeps them in the inflight counters.
		// Keep the old revision/time: a successor must already be coherent.
		remaining := *c
		remaining.AccountingHead = next.From
		remaining.PendingMessages -= count
		remaining.PendingBytes -= total
		remaining.WindowThrough = through
		if remaining.InflightCount == 0 {
			remaining.CompletedThrough = through
		}
		if next.From <= r.Through || ValidateMQTTAccountingHead(remaining, &next) != nil {
			return nil, false, dberrors.ErrCorruptValue
		}
	}
	c.AccountingHead = r.NextFrom
	if c.AccountingHead == 0 {
		c.AccountingTail = 0
	}
	return &r, true, nil
}

func (s *Shard) readMQTTAccounting(c MQTTDeliveryCursor) (*MQTTAccountingRange, error) {
	if c.AccountingHead == 0 {
		return nil, ValidateMQTTAccountingHead(c, nil)
	}
	key, err := mqttAccountingKey(s.hashSlot, c.Key, c.AccountingHead)
	if err != nil {
		return nil, err
	}
	// This helper is called only from the pinned compound read.
	if s.readSnapshot == nil {
		return nil, dberrors.ErrInvalidArgument
	}
	value, found, err := s.readSnapshot.Get(key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, dberrors.ErrCorruptValue
	}
	r, err := decodeMQTTAccountingRange(key, c.Key, c.AccountingHead, value)
	if err != nil {
		return nil, err
	}
	if err = ValidateMQTTAccountingHead(c, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
