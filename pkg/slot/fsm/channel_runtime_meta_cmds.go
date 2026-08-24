package fsm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	// MaxCreateChannelRuntimeMetaBatchItems bounds one command-59 Raft entry.
	MaxCreateChannelRuntimeMetaBatchItems = 64

	tagCreateChannelRuntimeMetaBatchEntry uint8 = 1
)

var createChannelRuntimeMetaResultMagic = [...]byte{'W', 'K', 'R', 'M', 2}

// CreateChannelRuntimeMetaBatchItem is one create-only row in command 59.
type CreateChannelRuntimeMetaBatchItem struct {
	// HashSlot is the logical hash slot that owns Meta.ChannelID.
	HashSlot uint16
	// Meta is the canonical runtime metadata candidate.
	Meta metadb.ChannelRuntimeMeta
}

// CreateChannelRuntimeMetaBatchResult is one identity-bound authoritative outcome.
type CreateChannelRuntimeMetaBatchResult struct {
	// HashSlot is the logical shard that committed the result.
	HashSlot uint16 `json:"hash_slot"`
	// ChannelID and ChannelType bind Created to one requested row.
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
	// Created distinguishes insertion from an idempotent concurrent loser.
	Created bool `json:"created"`
}

type createChannelRuntimeMetaBatchCmd struct {
	items   []CreateChannelRuntimeMetaBatchItem
	results []*metadb.ChannelRuntimeMetaCreateResult
}

func (c *createChannelRuntimeMetaBatchCmd) apply(wb *metadb.WriteBatch, _ uint16) error {
	c.results = make([]*metadb.ChannelRuntimeMetaCreateResult, len(c.items))
	for i, item := range c.items {
		result, err := wb.CreateChannelRuntimeMeta(item.HashSlot, item.Meta)
		if err != nil {
			return err
		}
		c.results[i] = result
		if item.Meta.ChannelType == 1 {
			if err := wb.EnsurePersonDirectoryTask(item.HashSlot, metadb.PersonDirectoryTask{
				ChannelID: item.Meta.ChannelID, ChannelType: item.Meta.ChannelType,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *createChannelRuntimeMetaBatchCmd) applyResult() []byte {
	results := make([]CreateChannelRuntimeMetaBatchResult, len(c.items))
	for i, item := range c.items {
		results[i] = CreateChannelRuntimeMetaBatchResult{
			HashSlot: item.HashSlot, ChannelID: item.Meta.ChannelID, ChannelType: item.Meta.ChannelType,
		}
		if i < len(c.results) && c.results[i] != nil {
			results[i].Created = c.results[i].Created
		}
	}
	return EncodeCreateChannelRuntimeMetaBatchResult(results)
}

func (c *createChannelRuntimeMetaBatchCmd) applyHashSlots(uint16) []uint16 {
	hashSlots := make([]uint16, 0, len(c.items))
	for _, item := range c.items {
		if len(hashSlots) == 0 || hashSlots[len(hashSlots)-1] != item.HashSlot {
			hashSlots = append(hashSlots, item.HashSlot)
		}
	}
	return hashSlots
}

// EncodeCreateChannelRuntimeMetaBatchCommandChecked validates, canonicalizes,
// and encodes one bounded command-59 batch.
func EncodeCreateChannelRuntimeMetaBatchCommandChecked(items []CreateChannelRuntimeMetaBatchItem) ([]byte, error) {
	canonical, err := canonicalCreateChannelRuntimeMetaBatch(items)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 0, headerSize+len(canonical)*160)
	buf = append(buf, commandVersion, cmdTypeCreateChannelRuntimeMeta)
	for _, item := range canonical {
		entry := make([]byte, 2)
		binary.BigEndian.PutUint16(entry, item.HashSlot)
		entry = append(entry, EncodeUpsertChannelRuntimeMetaCommand(item.Meta)...)
		buf = appendBytesTLVField(buf, tagCreateChannelRuntimeMetaBatchEntry, entry)
	}
	return buf, nil
}

func canonicalCreateChannelRuntimeMetaBatch(items []CreateChannelRuntimeMetaBatchItem) ([]CreateChannelRuntimeMetaBatchItem, error) {
	if len(items) == 0 || len(items) > MaxCreateChannelRuntimeMetaBatchItems {
		return nil, metadb.ErrInvalidArgument
	}
	canonical := make([]CreateChannelRuntimeMetaBatchItem, len(items))
	for i, item := range items {
		item.Meta = metadb.NormalizeChannelRuntimeMeta(item.Meta)
		if item.Meta.ChannelID == "" || item.Meta.ChannelType == 0 {
			return nil, metadb.ErrInvalidArgument
		}
		canonical[i] = item
	}
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].HashSlot != canonical[j].HashSlot {
			return canonical[i].HashSlot < canonical[j].HashSlot
		}
		if canonical[i].Meta.ChannelType != canonical[j].Meta.ChannelType {
			return canonical[i].Meta.ChannelType < canonical[j].Meta.ChannelType
		}
		return canonical[i].Meta.ChannelID < canonical[j].Meta.ChannelID
	})
	seen := make(map[string]struct{}, len(canonical))
	for _, item := range canonical {
		key := fmt.Sprintf("%d\x00%s", item.Meta.ChannelType, item.Meta.ChannelID)
		if _, ok := seen[key]; ok {
			return nil, metadb.ErrInvalidArgument
		}
		seen[key] = struct{}{}
	}
	return canonical, nil
}

// IsCreateChannelRuntimeMetaCommand reports whether data carries authoritative
// create-only runtime metadata outcomes.
func IsCreateChannelRuntimeMetaCommand(data []byte) bool {
	return len(data) >= 2 && data[0] == commandVersion &&
		(data[1] == cmdTypeCreateChannelRuntimeMeta || data[1] == cmdTypeAdmitPersonDirectoryTaskBatch)
}

// CreateChannelRuntimeMetaBatchCommandSize validates a create-bearing command
// and returns the number of logical create outcomes its authoritative future owns.
func CreateChannelRuntimeMetaBatchCommandSize(data []byte) (int, error) {
	if !IsCreateChannelRuntimeMetaCommand(data) {
		return 0, metadb.ErrInvalidArgument
	}
	decoded, err := decodeCommand(data)
	if err != nil {
		return 0, err
	}
	switch batch := decoded.(type) {
	case *createChannelRuntimeMetaBatchCmd:
		return len(batch.items), nil
	case *admitPersonDirectoryTaskBatchCmd:
		return len(batch.items), nil
	default:
		return 0, fmt.Errorf("%w: create runtime metadata batch", metadb.ErrCorruptValue)
	}
}

func decodeCreateChannelRuntimeMeta(data []byte) (command, error) {
	items := make([]CreateChannelRuntimeMetaBatchItem, 0, MaxCreateChannelRuntimeMetaBatchItems)
	for off := 0; off < len(data); {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		if tag != tagCreateChannelRuntimeMetaBatchEntry || len(value) < 4 {
			return nil, fmt.Errorf("%w: invalid create runtime metadata batch entry", metadb.ErrCorruptValue)
		}
		if len(items) == MaxCreateChannelRuntimeMetaBatchItems {
			return nil, fmt.Errorf("%w: oversized create runtime metadata batch", metadb.ErrCorruptValue)
		}
		hashSlot := binary.BigEndian.Uint16(value[:2])
		if value[2] != commandVersion || value[3] != cmdTypeUpsertChannelRuntimeMeta {
			return nil, fmt.Errorf("%w: invalid create runtime metadata record", metadb.ErrCorruptValue)
		}
		decoded, err := decodeUpsertChannelRuntimeMeta(value[4:])
		if err != nil {
			return nil, err
		}
		upsert, ok := decoded.(*upsertChannelRuntimeMetaCmd)
		if !ok {
			return nil, fmt.Errorf("%w: create runtime metadata payload", metadb.ErrCorruptValue)
		}
		items = append(items, CreateChannelRuntimeMetaBatchItem{HashSlot: hashSlot, Meta: upsert.meta})
	}
	canonical, err := canonicalCreateChannelRuntimeMetaBatch(items)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid create runtime metadata batch", metadb.ErrCorruptValue)
	}
	if !createRuntimeMetaItemsEqual(items, canonical) {
		return nil, fmt.Errorf("%w: non-canonical create runtime metadata batch", metadb.ErrCorruptValue)
	}
	return &createChannelRuntimeMetaBatchCmd{items: canonical}, nil
}

func createRuntimeMetaItemsEqual(left, right []CreateChannelRuntimeMetaBatchItem) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].HashSlot != right[i].HashSlot || left[i].Meta.ChannelID != right[i].Meta.ChannelID ||
			left[i].Meta.ChannelType != right[i].Meta.ChannelType {
			return false
		}
	}
	return true
}

// EncodeCreateChannelRuntimeMetaBatchResult encodes identity-bound outcomes.
func EncodeCreateChannelRuntimeMetaBatchResult(results []CreateChannelRuntimeMetaBatchResult) []byte {
	buf := append([]byte(nil), createChannelRuntimeMetaResultMagic[:]...)
	var count [2]byte
	binary.BigEndian.PutUint16(count[:], uint16(len(results)))
	buf = append(buf, count[:]...)
	for _, result := range results {
		var fixed [15]byte
		binary.BigEndian.PutUint16(fixed[0:2], result.HashSlot)
		binary.BigEndian.PutUint64(fixed[2:10], uint64(result.ChannelType))
		if result.Created {
			fixed[10] = 1
		}
		binary.BigEndian.PutUint32(fixed[11:15], uint32(len(result.ChannelID)))
		buf = append(buf, fixed[:]...)
		buf = append(buf, result.ChannelID...)
	}
	return buf
}

// DecodeCreateChannelRuntimeMetaBatchResult decodes identity-bound outcomes.
func DecodeCreateChannelRuntimeMetaBatchResult(data []byte) ([]CreateChannelRuntimeMetaBatchResult, error) {
	if len(data) < len(createChannelRuntimeMetaResultMagic)+2 ||
		!bytes.HasPrefix(data, createChannelRuntimeMetaResultMagic[:]) {
		return nil, fmt.Errorf("%w: create channel runtime metadata batch result", metadb.ErrCorruptValue)
	}
	off := len(createChannelRuntimeMetaResultMagic)
	count := int(binary.BigEndian.Uint16(data[off : off+2]))
	off += 2
	if count == 0 || count > MaxCreateChannelRuntimeMetaBatchItems {
		return nil, fmt.Errorf("%w: create channel runtime metadata batch result count", metadb.ErrCorruptValue)
	}
	results := make([]CreateChannelRuntimeMetaBatchResult, 0, count)
	for i := 0; i < count; i++ {
		if len(data)-off < 15 {
			return nil, fmt.Errorf("%w: truncated create channel runtime metadata batch result", metadb.ErrCorruptValue)
		}
		result := CreateChannelRuntimeMetaBatchResult{
			HashSlot:    binary.BigEndian.Uint16(data[off : off+2]),
			ChannelType: int64(binary.BigEndian.Uint64(data[off+2 : off+10])),
		}
		switch data[off+10] {
		case 0:
		case 1:
			result.Created = true
		default:
			return nil, fmt.Errorf("%w: invalid create channel runtime metadata batch result value", metadb.ErrCorruptValue)
		}
		channelIDLen := int(binary.BigEndian.Uint32(data[off+11 : off+15]))
		off += 15
		if channelIDLen == 0 || channelIDLen > len(data)-off {
			return nil, fmt.Errorf("%w: invalid create channel runtime metadata batch result identity", metadb.ErrCorruptValue)
		}
		result.ChannelID = string(data[off : off+channelIDLen])
		off += channelIDLen
		results = append(results, result)
	}
	if off != len(data) {
		return nil, fmt.Errorf("%w: trailing create channel runtime metadata batch result", metadb.ErrCorruptValue)
	}
	return results, nil
}
