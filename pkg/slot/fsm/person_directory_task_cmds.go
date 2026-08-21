package fsm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

const (
	// MaxPersonDirectoryBatchItems bounds one multi-hash-slot directory command.
	MaxPersonDirectoryBatchItems        = 128
	maxPersonDirectoryBatchBytes        = 256 << 10
	maxPreparePersonDirectoryBatchBytes = 512 << 10

	tagPersonDirectoryTaskBatchEntry = 1
	personDirectoryAdmissionPrefix   = 18
)

// UserChannelMembershipBatchItem binds one membership row to its logical hash slot.
type UserChannelMembershipBatchItem struct {
	HashSlot   uint16
	Membership metadb.UserChannelMembership
}

// PersonDirectoryAdmissionBatchItem binds one task and its create-only
// runtime metadata to the same source hash slot.
type PersonDirectoryAdmissionBatchItem struct {
	HashSlot    uint16
	Task        metadb.PersonDirectoryTask
	RuntimeMeta metadb.ChannelRuntimeMeta
}

// PersonDirectoryCompletionBatchItem identifies one source-owned task.
type PersonDirectoryCompletionBatchItem struct {
	HashSlot    uint16
	ChannelID   string
	ChannelType int64
	Generation  uint64
}

type admitPersonDirectoryTaskBatchCmd struct {
	items   []PersonDirectoryAdmissionBatchItem
	results []*metadb.ChannelRuntimeMetaCreateResult
}

func (c *admitPersonDirectoryTaskBatchCmd) apply(wb *metadb.WriteBatch, _ uint16) error {
	c.results = make([]*metadb.ChannelRuntimeMetaCreateResult, len(c.items))
	for i, item := range c.items {
		result, err := wb.CreateChannelRuntimeMeta(item.HashSlot, item.RuntimeMeta)
		if err != nil {
			return err
		}
		c.results[i] = result
		if err := wb.EnsurePersonDirectoryTask(item.HashSlot, item.Task); err != nil {
			return err
		}
	}
	return nil
}

func (c *admitPersonDirectoryTaskBatchCmd) applyForHashSlot(wb *metadb.WriteBatch, hashSlot uint16) error {
	if len(c.results) != len(c.items) {
		c.results = make([]*metadb.ChannelRuntimeMetaCreateResult, len(c.items))
	}
	for i, item := range c.items {
		if item.HashSlot != hashSlot {
			continue
		}
		result, err := wb.CreateChannelRuntimeMeta(item.HashSlot, item.RuntimeMeta)
		if err != nil {
			return err
		}
		c.results[i] = result
		if err := wb.EnsurePersonDirectoryTask(item.HashSlot, item.Task); err != nil {
			return err
		}
	}
	return nil
}

func (c *admitPersonDirectoryTaskBatchCmd) applyHashSlots(uint16) []uint16 {
	return admissionHashSlots(c.items)
}

func (c *admitPersonDirectoryTaskBatchCmd) applyResult() []byte {
	results := make([]CreateChannelRuntimeMetaBatchResult, len(c.items))
	for i, item := range c.items {
		results[i] = CreateChannelRuntimeMetaBatchResult{HashSlot: item.HashSlot, ChannelID: item.Task.ChannelID, ChannelType: item.Task.ChannelType}
		if i < len(c.results) && c.results[i] != nil {
			results[i].Created = c.results[i].Created
		}
	}
	return EncodeCreateChannelRuntimeMetaBatchResult(results)
}

type ensureUserChannelMembershipBatchCmd struct {
	items []UserChannelMembershipBatchItem
}

func (c *ensureUserChannelMembershipBatchCmd) apply(wb *metadb.WriteBatch, _ uint16) error {
	for _, item := range c.items {
		if err := wb.EnsureUserChannelMembership(item.HashSlot, item.Membership); err != nil {
			return err
		}
	}
	return nil
}

func (c *ensureUserChannelMembershipBatchCmd) applyForHashSlot(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, item := range c.items {
		if item.HashSlot == hashSlot {
			if err := wb.EnsureUserChannelMembership(item.HashSlot, item.Membership); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *ensureUserChannelMembershipBatchCmd) applyHashSlots(uint16) []uint16 {
	return membershipBatchHashSlots(c.items)
}

type completePersonDirectoryTaskBatchCmd struct {
	items []PersonDirectoryCompletionBatchItem
}

func (c *completePersonDirectoryTaskBatchCmd) apply(wb *metadb.WriteBatch, _ uint16) error {
	for _, item := range c.items {
		if err := wb.CompletePersonDirectoryTask(item.HashSlot, metadb.PersonDirectoryTaskLocation{HashSlot: item.HashSlot, ChannelID: item.ChannelID, ChannelType: item.ChannelType, Generation: item.Generation}); err != nil {
			return err
		}
	}
	return nil
}

func (c *completePersonDirectoryTaskBatchCmd) applyForHashSlot(wb *metadb.WriteBatch, hashSlot uint16) error {
	for _, item := range c.items {
		if item.HashSlot == hashSlot {
			if err := wb.CompletePersonDirectoryTask(item.HashSlot, metadb.PersonDirectoryTaskLocation{HashSlot: item.HashSlot, ChannelID: item.ChannelID, ChannelType: item.ChannelType, Generation: item.Generation}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *completePersonDirectoryTaskBatchCmd) applyHashSlots(uint16) []uint16 {
	hashSlots := make([]uint16, 0, len(c.items))
	for _, item := range c.items {
		if len(hashSlots) == 0 || hashSlots[len(hashSlots)-1] != item.HashSlot {
			hashSlots = append(hashSlots, item.HashSlot)
		}
	}
	return hashSlots
}

// EncodeAdmitPersonDirectoryTaskBatchCommandChecked encodes source-Slot task
// admission together with create-only Channel runtime metadata.
func EncodeAdmitPersonDirectoryTaskBatchCommandChecked(items []PersonDirectoryAdmissionBatchItem) ([]byte, error) {
	canonical, err := canonicalAdmissionBatch(items)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 0, headerSize+len(canonical)*192)
	buf = append(buf, commandVersion, cmdTypeAdmitPersonDirectoryTaskBatch)
	for _, item := range canonical {
		entry := make([]byte, personDirectoryAdmissionPrefix)
		binary.BigEndian.PutUint16(entry[:2], item.HashSlot)
		binary.BigEndian.PutUint64(entry[2:10], item.Task.CommittedTail)
		binary.BigEndian.PutUint64(entry[10:18], uint64(item.Task.CreatedAt))
		entry = append(entry, EncodeUpsertChannelRuntimeMetaCommand(item.RuntimeMeta)...)
		buf = appendBytesTLVField(buf, tagPersonDirectoryTaskBatchEntry, entry)
	}
	if len(buf) > maxPreparePersonDirectoryBatchBytes {
		return nil, metadb.ErrInvalidArgument
	}
	return buf, nil
}

// EncodeEnsureUserChannelMembershipBatchCommandChecked encodes bounded
// create-if-absent person memberships routed by UID hash slot.
func EncodeEnsureUserChannelMembershipBatchCommandChecked(items []UserChannelMembershipBatchItem) ([]byte, error) {
	canonical, err := canonicalPersonMembershipBatch(items)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 0, headerSize+len(canonical)*128)
	buf = append(buf, commandVersion, cmdTypeEnsureUserChannelMembershipBatch)
	for _, item := range canonical {
		entry := make([]byte, 2)
		binary.BigEndian.PutUint16(entry, item.HashSlot)
		entry = append(entry, encodeUserChannelMembershipEntry(item.Membership, true)...)
		buf = appendBytesTLVField(buf, tagPersonDirectoryTaskBatchEntry, entry)
	}
	if len(buf) > maxPersonDirectoryBatchBytes {
		return nil, metadb.ErrInvalidArgument
	}
	return buf, nil
}

// EncodeCompletePersonDirectoryTaskBatchCommandChecked encodes atomic source
// task deletion and pending-to-ready transitions.
func EncodeCompletePersonDirectoryTaskBatchCommandChecked(items []PersonDirectoryCompletionBatchItem) ([]byte, error) {
	canonical, err := canonicalCompletionBatch(items)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 0, headerSize+len(canonical)*64)
	buf = append(buf, commandVersion, cmdTypeCompletePersonDirectoryTaskBatch)
	for _, item := range canonical {
		entry := make([]byte, 10)
		binary.BigEndian.PutUint16(entry, item.HashSlot)
		binary.BigEndian.PutUint64(entry[2:10], item.Generation)
		channel := encodeChannelCommand(cmdTypeCompletePersonDirectoryTaskBatch, metadb.Channel{ChannelID: item.ChannelID, ChannelType: item.ChannelType})
		entry = append(entry, channel[headerSize:]...)
		buf = appendBytesTLVField(buf, tagPersonDirectoryTaskBatchEntry, entry)
	}
	if len(buf) > maxPersonDirectoryBatchBytes {
		return nil, metadb.ErrInvalidArgument
	}
	return buf, nil
}

func decodeAdmitPersonDirectoryTaskBatch(data []byte) (command, error) {
	if len(data)+headerSize > maxPreparePersonDirectoryBatchBytes {
		return nil, metadb.ErrInvalidArgument
	}
	items := make([]PersonDirectoryAdmissionBatchItem, 0, MaxPersonDirectoryBatchItems)
	for off := 0; off < len(data); {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		if tag != tagPersonDirectoryTaskBatchEntry || len(value) < personDirectoryAdmissionPrefix+2 || len(items) == MaxPersonDirectoryBatchItems {
			return nil, metadb.ErrInvalidArgument
		}
		if value[personDirectoryAdmissionPrefix] != commandVersion || value[personDirectoryAdmissionPrefix+1] != cmdTypeUpsertChannelRuntimeMeta {
			return nil, metadb.ErrInvalidArgument
		}
		decoded, err := decodeUpsertChannelRuntimeMeta(value[personDirectoryAdmissionPrefix+2:])
		if err != nil {
			return nil, err
		}
		upsert, ok := decoded.(*upsertChannelRuntimeMetaCmd)
		if !ok {
			return nil, metadb.ErrCorruptValue
		}
		items = append(items, PersonDirectoryAdmissionBatchItem{
			HashSlot: binary.BigEndian.Uint16(value[:2]),
			Task: metadb.PersonDirectoryTask{ChannelID: upsert.meta.ChannelID, ChannelType: upsert.meta.ChannelType,
				CommittedTail: binary.BigEndian.Uint64(value[2:10]), CreatedAt: int64(binary.BigEndian.Uint64(value[10:18]))},
			RuntimeMeta: upsert.meta,
		})
	}
	canonical, err := canonicalAdmissionBatch(items)
	if err != nil {
		return nil, err
	}
	reencoded, err := EncodeAdmitPersonDirectoryTaskBatchCommandChecked(canonical)
	if err != nil {
		return nil, err
	}
	original := append([]byte{commandVersion, cmdTypeAdmitPersonDirectoryTaskBatch}, data...)
	if !bytes.Equal(original, reencoded) {
		return nil, fmt.Errorf("%w: non-canonical person directory admission batch", metadb.ErrCorruptValue)
	}
	return &admitPersonDirectoryTaskBatchCmd{items: canonical}, nil
}

func decodeEnsureUserChannelMembershipBatch(data []byte) (command, error) {
	items, err := decodeMembershipBatchEntries(data)
	if err != nil {
		return nil, err
	}
	canonical, err := canonicalPersonMembershipBatch(items)
	if err != nil {
		return nil, err
	}
	if !membershipBatchItemsEqual(items, canonical) {
		return nil, fmt.Errorf("%w: non-canonical ensured membership batch", metadb.ErrCorruptValue)
	}
	return &ensureUserChannelMembershipBatchCmd{items: canonical}, nil
}

func decodeCompletePersonDirectoryTaskBatch(data []byte) (command, error) {
	if len(data)+headerSize > maxPersonDirectoryBatchBytes {
		return nil, metadb.ErrInvalidArgument
	}
	items := make([]PersonDirectoryCompletionBatchItem, 0, MaxPersonDirectoryBatchItems)
	for off := 0; off < len(data); {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		if tag != tagPersonDirectoryTaskBatchEntry || len(value) < 10 || len(items) == MaxPersonDirectoryBatchItems {
			return nil, metadb.ErrInvalidArgument
		}
		channel, err := decodeChannel(value[10:])
		if err != nil {
			return nil, err
		}
		items = append(items, PersonDirectoryCompletionBatchItem{HashSlot: binary.BigEndian.Uint16(value[:2]), ChannelID: channel.ChannelID, ChannelType: channel.ChannelType, Generation: binary.BigEndian.Uint64(value[2:10])})
	}
	canonical, err := canonicalCompletionBatch(items)
	if err != nil {
		return nil, err
	}
	if !completionItemsEqual(items, canonical) {
		return nil, fmt.Errorf("%w: non-canonical person directory completion batch", metadb.ErrCorruptValue)
	}
	return &completePersonDirectoryTaskBatchCmd{items: canonical}, nil
}

func decodeMembershipBatchEntries(data []byte) ([]UserChannelMembershipBatchItem, error) {
	if len(data)+headerSize > maxPersonDirectoryBatchBytes {
		return nil, metadb.ErrInvalidArgument
	}
	items := make([]UserChannelMembershipBatchItem, 0, MaxPersonDirectoryBatchItems)
	for off := 0; off < len(data); {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		if tag != tagPersonDirectoryTaskBatchEntry || len(value) < 2 || len(items) == MaxPersonDirectoryBatchItems {
			return nil, metadb.ErrInvalidArgument
		}
		membership, err := decodeUserChannelMembershipEntry(value[2:], true)
		if err != nil {
			return nil, err
		}
		items = append(items, UserChannelMembershipBatchItem{HashSlot: binary.BigEndian.Uint16(value[:2]), Membership: membership})
	}
	return items, nil
}

func canonicalAdmissionBatch(items []PersonDirectoryAdmissionBatchItem) ([]PersonDirectoryAdmissionBatchItem, error) {
	if len(items) == 0 || len(items) > MaxPersonDirectoryBatchItems {
		return nil, metadb.ErrInvalidArgument
	}
	canonical := append([]PersonDirectoryAdmissionBatchItem(nil), items...)
	metas := make([]CreateChannelRuntimeMetaBatchItem, len(canonical))
	for i := range canonical {
		item := &canonical[i]
		item.RuntimeMeta = metadb.NormalizeChannelRuntimeMeta(item.RuntimeMeta)
		if item.Task.ChannelID != item.RuntimeMeta.ChannelID || item.Task.ChannelType != item.RuntimeMeta.ChannelType || item.Task.CreatedAt < 0 || !canonicalPersonChannel(item.Task.ChannelID, item.Task.ChannelType) {
			return nil, metadb.ErrInvalidArgument
		}
		metas[i] = CreateChannelRuntimeMetaBatchItem{HashSlot: item.HashSlot, Meta: item.RuntimeMeta}
	}
	if _, err := canonicalCreateChannelRuntimeMetaBatch(metas); err != nil {
		return nil, err
	}
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].HashSlot != canonical[j].HashSlot {
			return canonical[i].HashSlot < canonical[j].HashSlot
		}
		if canonical[i].Task.ChannelType != canonical[j].Task.ChannelType {
			return canonical[i].Task.ChannelType < canonical[j].Task.ChannelType
		}
		return canonical[i].Task.ChannelID < canonical[j].Task.ChannelID
	})
	for i := 1; i < len(canonical); i++ {
		if canonical[i-1].HashSlot == canonical[i].HashSlot && canonical[i-1].Task.ChannelID == canonical[i].Task.ChannelID && canonical[i-1].Task.ChannelType == canonical[i].Task.ChannelType {
			return nil, metadb.ErrInvalidArgument
		}
	}
	return canonical, nil
}

func canonicalPersonMembershipBatch(items []UserChannelMembershipBatchItem) ([]UserChannelMembershipBatchItem, error) {
	canonical, err := canonicalMembershipBatch(items)
	if err != nil {
		return nil, err
	}
	for _, item := range canonical {
		left, right, err := runtimechannelid.DecodePersonChannel(item.Membership.ChannelID)
		if err != nil || runtimechannelid.EncodePersonChannel(left, right) != item.Membership.ChannelID || item.Membership.ChannelType != 1 || (item.Membership.UID != left && item.Membership.UID != right) {
			return nil, metadb.ErrInvalidArgument
		}
	}
	return canonical, nil
}

func canonicalCompletionBatch(items []PersonDirectoryCompletionBatchItem) ([]PersonDirectoryCompletionBatchItem, error) {
	if len(items) == 0 || len(items) > MaxPersonDirectoryBatchItems {
		return nil, metadb.ErrInvalidArgument
	}
	canonical := append([]PersonDirectoryCompletionBatchItem(nil), items...)
	for _, item := range canonical {
		if !canonicalPersonChannel(item.ChannelID, item.ChannelType) || item.Generation == 0 {
			return nil, metadb.ErrInvalidArgument
		}
	}
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].HashSlot != canonical[j].HashSlot {
			return canonical[i].HashSlot < canonical[j].HashSlot
		}
		if canonical[i].ChannelType != canonical[j].ChannelType {
			return canonical[i].ChannelType < canonical[j].ChannelType
		}
		return canonical[i].ChannelID < canonical[j].ChannelID
	})
	for i := 1; i < len(canonical); i++ {
		if canonical[i-1].HashSlot == canonical[i].HashSlot && canonical[i-1].ChannelID == canonical[i].ChannelID && canonical[i-1].ChannelType == canonical[i].ChannelType {
			return nil, metadb.ErrInvalidArgument
		}
	}
	return canonical, nil
}

func canonicalPersonChannel(channelID string, channelType int64) bool {
	left, right, err := runtimechannelid.DecodePersonChannel(channelID)
	return err == nil && channelType == 1 && runtimechannelid.EncodePersonChannel(left, right) == channelID
}

func admissionHashSlots(items []PersonDirectoryAdmissionBatchItem) []uint16 {
	result := make([]uint16, 0, len(items))
	for _, item := range items {
		if len(result) == 0 || result[len(result)-1] != item.HashSlot {
			result = append(result, item.HashSlot)
		}
	}
	return result
}

func completionItemsEqual(left, right []PersonDirectoryCompletionBatchItem) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func canonicalMembershipBatch(items []UserChannelMembershipBatchItem) ([]UserChannelMembershipBatchItem, error) {
	if len(items) == 0 || len(items) > MaxPersonDirectoryBatchItems {
		return nil, metadb.ErrInvalidArgument
	}
	canonical := append([]UserChannelMembershipBatchItem(nil), items...)
	for _, item := range canonical {
		if item.Membership.UID == "" || item.Membership.ChannelID == "" || item.Membership.ChannelType <= 0 {
			return nil, metadb.ErrInvalidArgument
		}
	}
	if err := ValidateSubscriberCommandLimits(userChannelMembershipBatchUIDs(canonical)); err != nil {
		return nil, err
	}
	sort.Slice(canonical, func(i, j int) bool {
		left, right := canonical[i], canonical[j]
		if left.HashSlot != right.HashSlot {
			return left.HashSlot < right.HashSlot
		}
		if left.Membership.UID != right.Membership.UID {
			return left.Membership.UID < right.Membership.UID
		}
		if left.Membership.ChannelType != right.Membership.ChannelType {
			return left.Membership.ChannelType < right.Membership.ChannelType
		}
		return left.Membership.ChannelID < right.Membership.ChannelID
	})
	for i := 1; i < len(canonical); i++ {
		left, right := canonical[i-1], canonical[i]
		if left.HashSlot == right.HashSlot && left.Membership.UID == right.Membership.UID && left.Membership.ChannelID == right.Membership.ChannelID && left.Membership.ChannelType == right.Membership.ChannelType {
			return nil, metadb.ErrInvalidArgument
		}
	}
	return canonical, nil
}

func userChannelMembershipBatchUIDs(items []UserChannelMembershipBatchItem) []string {
	uids := make([]string, len(items))
	for i, item := range items {
		uids[i] = item.Membership.UID
	}
	return uids
}

func membershipBatchHashSlots(items []UserChannelMembershipBatchItem) []uint16 {
	hashSlots := make([]uint16, 0, len(items))
	for _, item := range items {
		if len(hashSlots) == 0 || hashSlots[len(hashSlots)-1] != item.HashSlot {
			hashSlots = append(hashSlots, item.HashSlot)
		}
	}
	return hashSlots
}

func membershipBatchItemsEqual(left, right []UserChannelMembershipBatchItem) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].HashSlot != right[i].HashSlot || left[i].Membership != right[i].Membership {
			return false
		}
	}
	return true
}
