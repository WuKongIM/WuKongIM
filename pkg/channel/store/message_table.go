package store

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

// messageTable provides channel-scoped structured message row storage.
type messageTable struct {
	channelKey channel.ChannelKey
	db         *pebble.DB
}

type messageIdempotencyKey struct {
	fromUID     string
	clientMsgNo string
}

func (t *messageTable) append(writeBatch *pebble.Batch, rows []messageRow) error {
	if err := t.validate(); err != nil {
		return err
	}
	if writeBatch == nil {
		return channel.ErrInvalidArgument
	}

	seenMessageIDs := make(map[uint64]struct{}, len(rows))
	seenIdempotencyKeys := make(map[messageIdempotencyKey]struct{}, len(rows))
	for _, row := range rows {
		if row.MessageSeq == 0 {
			return channel.ErrInvalidArgument
		}
		if _, ok := seenMessageIDs[row.MessageID]; ok {
			return channel.ErrCorruptState
		}
		seenMessageIDs[row.MessageID] = struct{}{}

		if existingSeq, ok, err := t.lookupMessageIDSeq(row.MessageID); err != nil {
			return err
		} else if ok && existingSeq != row.MessageSeq {
			return channel.ErrCorruptState
		}

		primary, payload, err := encodeMessageFamilies(row)
		if err != nil {
			return err
		}
		if err := writeBatch.Set(encodeTableStateKey(t.channelKey, TableIDMessage, row.MessageSeq, messagePrimaryFamilyID), primary, pebble.NoSync); err != nil {
			return err
		}
		if err := writeBatch.Set(encodeTableStateKey(t.channelKey, TableIDMessage, row.MessageSeq, messagePayloadFamilyID), payload, pebble.NoSync); err != nil {
			return err
		}
		if err := writeBatch.Set(encodeMessageIDIndexKey(t.channelKey, row.MessageID), encodeMessageIDIndexValue(row.MessageSeq), pebble.NoSync); err != nil {
			return err
		}
		if row.ClientMsgNo != "" {
			if err := writeBatch.Set(encodeMessageClientMsgNoIndexKey(t.channelKey, row.ClientMsgNo, row.MessageSeq), encodeMessageIDIndexValue(row.MessageSeq), pebble.NoSync); err != nil {
				return err
			}
		}
		if row.FromUID != "" && row.ClientMsgNo != "" {
			key := messageIdempotencyKey{fromUID: row.FromUID, clientMsgNo: row.ClientMsgNo}
			if _, ok := seenIdempotencyKeys[key]; ok {
				return channel.ErrCorruptState
			}
			seenIdempotencyKeys[key] = struct{}{}
			if existing, ok, err := t.lookupIdempotency(row.FromUID, row.ClientMsgNo); err != nil {
				return err
			} else if ok && existing.MessageSeq != row.MessageSeq {
				return channel.ErrCorruptState
			}
			value, err := encodeIdempotencyIndexValue(row)
			if err != nil {
				return err
			}
			if err := writeBatch.Set(encodeMessageIdempotencyIndexKey(t.channelKey, row.FromUID, row.ClientMsgNo), value, pebble.NoSync); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *messageTable) getBySeq(seq uint64) (messageRow, bool, error) {
	if err := t.validate(); err != nil {
		return messageRow{}, false, err
	}
	if seq == 0 {
		return messageRow{}, false, channel.ErrInvalidArgument
	}

	primary, okPrimary, err := t.getValue(encodeTableStateKey(t.channelKey, TableIDMessage, seq, messagePrimaryFamilyID))
	if err != nil {
		return messageRow{}, false, err
	}
	payload, okPayload, err := t.getValue(encodeTableStateKey(t.channelKey, TableIDMessage, seq, messagePayloadFamilyID))
	if err != nil {
		return messageRow{}, false, err
	}
	if !okPrimary && !okPayload {
		return messageRow{}, false, nil
	}
	if !okPrimary || !okPayload {
		return messageRow{}, false, channel.ErrCorruptState
	}
	row, err := decodeMessageFamilies(seq, primary, payload)
	if err != nil {
		return messageRow{}, false, err
	}
	if row.PayloadHash != hashMessagePayload(row.Payload) {
		return messageRow{}, false, channel.ErrCorruptState
	}
	return row, true, nil
}

func (t *messageTable) getByMessageID(messageID uint64) (messageRow, bool, error) {
	if err := t.validate(); err != nil {
		return messageRow{}, false, err
	}
	if messageID == 0 {
		return messageRow{}, false, channel.ErrInvalidArgument
	}

	seq, ok, err := t.lookupMessageIDSeq(messageID)
	if err != nil || !ok {
		return messageRow{}, ok, err
	}
	row, ok, err := t.getBySeq(seq)
	if err != nil || !ok {
		if err != nil {
			return messageRow{}, false, err
		}
		return messageRow{}, false, channel.ErrCorruptState
	}
	if row.MessageID != messageID {
		return messageRow{}, false, channel.ErrCorruptState
	}
	return row, true, nil
}

func (t *messageTable) scanBySeq(fromSeq uint64, limit int, maxBytes int) ([]messageRow, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	if limit <= 0 || maxBytes <= 0 {
		return nil, nil
	}
	if fromSeq == 0 {
		fromSeq = 1
	}

	prefix := encodeTableStatePrefix(t.channelKey, TableIDMessage)
	iter, err := t.db.NewIter(&pebble.IterOptions{
		LowerBound: encodeTableStateKey(t.channelKey, TableIDMessage, fromSeq, messagePrimaryFamilyID),
		UpperBound: keyUpperBound(prefix),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	rows := make([]messageRow, 0, minInt(limit, logScanInitialCapacity))
	total := 0
	var skipPayloadSeq uint64
	for valid := iter.First(); valid && len(rows) < limit; valid = iter.Next() {
		seq, familyID, err := decodeTableStateKey(iter.Key(), t.channelKey, TableIDMessage)
		if err != nil {
			return nil, err
		}
		processRow, err := consumeForwardMessageFamily(seq, familyID, &skipPayloadSeq)
		if err != nil {
			return nil, err
		}
		if !processRow {
			continue
		}
		row, err := t.materializeRow(seq, iter.Value())
		if err != nil {
			return nil, err
		}
		size, err := row.compatibilityEncodedRecordSize()
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 && total+size > maxBytes {
			break
		}
		rows = append(rows, row)
		total += size
		skipPayloadSeq = seq
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return rows, nil
}

func (t *messageTable) scanBySeqReverse(fromSeq uint64, limit int, maxBytes int) ([]messageRow, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	if limit <= 0 || maxBytes <= 0 || fromSeq == 0 {
		return nil, nil
	}

	prefix := encodeTableStatePrefix(t.channelKey, TableIDMessage)
	upperBound := keyUpperBound(prefix)
	seekKey := upperBound
	if fromSeq < math.MaxUint64 {
		seekKey = encodeTableStateKey(t.channelKey, TableIDMessage, fromSeq+1, messagePrimaryFamilyID)
	}

	iter, err := t.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: upperBound})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	rows := make([]messageRow, 0, minInt(limit, logScanInitialCapacity))
	total := 0
	var pendingPayloadSeq uint64
	for valid := iter.SeekLT(seekKey); valid && len(rows) < limit; valid = iter.Prev() {
		seq, familyID, err := decodeTableStateKey(iter.Key(), t.channelKey, TableIDMessage)
		if err != nil {
			return nil, err
		}
		processRow, err := consumeReverseMessageFamily(seq, familyID, &pendingPayloadSeq)
		if err != nil {
			return nil, err
		}
		if !processRow {
			continue
		}
		row, err := t.materializeRow(seq, iter.Value())
		if err != nil {
			return nil, err
		}
		size, err := row.compatibilityEncodedRecordSize()
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 && total+size > maxBytes {
			break
		}
		rows = append(rows, row)
		total += size
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	if pendingPayloadSeq != 0 {
		return nil, channel.ErrCorruptState
	}
	return rows, nil
}

func (t *messageTable) scanByClientMsgNo(clientMsgNo string, beforeSeq uint64, limit int) ([]messageRow, uint64, bool, error) {
	if err := t.validate(); err != nil {
		return nil, 0, false, err
	}
	if clientMsgNo == "" || limit <= 0 {
		return nil, 0, false, channel.ErrInvalidArgument
	}

	prefix := encodeMessageClientMsgNoIndexPrefix(t.channelKey, clientMsgNo)
	upperBound := keyUpperBound(prefix)
	seekKey := upperBound
	if beforeSeq > 0 {
		seekKey = encodeMessageClientMsgNoIndexKey(t.channelKey, clientMsgNo, beforeSeq)
	}

	iter, err := t.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: upperBound})
	if err != nil {
		return nil, 0, false, err
	}
	defer iter.Close()

	rows := make([]messageRow, 0, limit)
	hasMore := false
	for valid := iter.SeekLT(seekKey); valid; valid = iter.Prev() {
		seq, err := decodeMessageClientMsgNoIndexSeq(iter.Key(), t.channelKey, clientMsgNo)
		if err != nil {
			return nil, 0, false, err
		}
		row, ok, err := t.getBySeq(seq)
		if err != nil {
			return nil, 0, false, err
		}
		if !ok || row.ClientMsgNo != clientMsgNo {
			return nil, 0, false, channel.ErrCorruptState
		}
		if len(rows) == limit {
			hasMore = true
			break
		}
		rows = append(rows, row)
	}
	if err := iter.Error(); err != nil {
		return nil, 0, false, err
	}
	if !hasMore || len(rows) == 0 {
		return rows, 0, false, nil
	}
	return rows, rows[len(rows)-1].MessageSeq, true, nil
}

func (t *messageTable) truncateFromSeq(writeBatch *pebble.Batch, fromSeq uint64) error {
	if err := t.validate(); err != nil {
		return err
	}
	if writeBatch == nil {
		return channel.ErrInvalidArgument
	}
	if fromSeq == 0 {
		fromSeq = 1
	}

	prefix := encodeTableStatePrefix(t.channelKey, TableIDMessage)
	iter, err := t.db.NewIter(&pebble.IterOptions{
		LowerBound: encodeTableStateKey(t.channelKey, TableIDMessage, fromSeq, messagePrimaryFamilyID),
		UpperBound: keyUpperBound(prefix),
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	var skipPayloadSeq uint64
	for valid := iter.First(); valid; valid = iter.Next() {
		seq, familyID, err := decodeTableStateKey(iter.Key(), t.channelKey, TableIDMessage)
		if err != nil {
			return err
		}
		processRow, err := consumeForwardMessageFamily(seq, familyID, &skipPayloadSeq)
		if err != nil {
			return err
		}
		if !processRow {
			continue
		}
		row, err := t.materializeRow(seq, iter.Value())
		if err != nil {
			return err
		}
		if err := writeBatch.Delete(encodeTableStateKey(t.channelKey, TableIDMessage, row.MessageSeq, messagePrimaryFamilyID), pebble.NoSync); err != nil {
			return err
		}
		if err := writeBatch.Delete(encodeTableStateKey(t.channelKey, TableIDMessage, row.MessageSeq, messagePayloadFamilyID), pebble.NoSync); err != nil {
			return err
		}
		if err := writeBatch.Delete(encodeMessageIDIndexKey(t.channelKey, row.MessageID), pebble.NoSync); err != nil {
			return err
		}
		if row.ClientMsgNo != "" {
			if err := writeBatch.Delete(encodeMessageClientMsgNoIndexKey(t.channelKey, row.ClientMsgNo, row.MessageSeq), pebble.NoSync); err != nil {
				return err
			}
		}
		if row.FromUID != "" && row.ClientMsgNo != "" {
			if err := writeBatch.Delete(encodeMessageIdempotencyIndexKey(t.channelKey, row.FromUID, row.ClientMsgNo), pebble.NoSync); err != nil {
				return err
			}
		}
		skipPayloadSeq = seq
	}
	return iter.Error()
}

func (t *messageTable) lookupIdempotency(fromUID, clientMsgNo string) (messageIndexHit, bool, error) {
	if err := t.validate(); err != nil {
		return messageIndexHit{}, false, err
	}
	if fromUID == "" || clientMsgNo == "" {
		return messageIndexHit{}, false, channel.ErrInvalidArgument
	}

	value, ok, err := t.getValue(encodeMessageIdempotencyIndexKey(t.channelKey, fromUID, clientMsgNo))
	if err != nil || !ok {
		return messageIndexHit{}, ok, err
	}
	hit, err := decodeIdempotencyIndexValue(value)
	if err != nil {
		return messageIndexHit{}, false, err
	}
	row, ok, err := t.getBySeq(hit.MessageSeq)
	if err != nil {
		return messageIndexHit{}, false, err
	}
	if !ok || row.MessageID != hit.MessageID || row.PayloadHash != hit.PayloadHash || row.FromUID != fromUID || row.ClientMsgNo != clientMsgNo {
		return messageIndexHit{}, false, channel.ErrCorruptState
	}
	return hit, true, nil
}

func (t *messageTable) maxSeq() (uint64, error) {
	if err := t.validate(); err != nil {
		return 0, err
	}

	prefix := encodeTableStatePrefix(t.channelKey, TableIDMessage)
	iter, err := t.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: keyUpperBound(prefix)})
	if err != nil {
		return 0, err
	}
	defer iter.Close()
	if !iter.Last() {
		return 0, nil
	}
	seq, _, err := decodeTableStateKey(iter.Key(), t.channelKey, TableIDMessage)
	if err != nil {
		return 0, err
	}
	if _, ok, err := t.getBySeq(seq); err != nil {
		return 0, err
	} else if !ok {
		return 0, channel.ErrCorruptState
	}
	return seq, nil
}

func (t *messageTable) lookupMessageIDSeq(messageID uint64) (uint64, bool, error) {
	value, ok, err := t.getValue(encodeMessageIDIndexKey(t.channelKey, messageID))
	if err != nil || !ok {
		return 0, ok, err
	}
	seq, err := decodeMessageIDIndexValue(value)
	if err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

func (t *messageTable) materializeRow(seq uint64, primary []byte) (messageRow, error) {
	payload, ok, err := t.getValue(encodeTableStateKey(t.channelKey, TableIDMessage, seq, messagePayloadFamilyID))
	if err != nil {
		return messageRow{}, err
	}
	if !ok {
		return messageRow{}, channel.ErrCorruptState
	}
	row, err := decodeMessageFamilies(seq, append([]byte(nil), primary...), payload)
	if err != nil {
		return messageRow{}, err
	}
	if row.PayloadHash != hashMessagePayload(row.Payload) {
		return messageRow{}, channel.ErrCorruptState
	}
	return row, nil
}

func (t *messageTable) getValue(key []byte) ([]byte, bool, error) {
	value, closer, err := t.db.Get(key)
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer closer.Close()
	return append([]byte(nil), value...), true, nil
}

func (t *messageTable) validate() error {
	if t == nil || t.db == nil || t.channelKey == "" {
		return channel.ErrInvalidArgument
	}
	return nil
}

func encodeMessageIDIndexKey(channelKey channel.ChannelKey, messageID uint64) []byte {
	key := encodeTableIndexPrefix(channelKey, TableIDMessage, messageIndexIDMessageID)
	return binary.BigEndian.AppendUint64(key, messageID)
}

func encodeMessageClientMsgNoIndexPrefix(channelKey channel.ChannelKey, clientMsgNo string) []byte {
	key := encodeTableIndexPrefix(channelKey, TableIDMessage, messageIndexIDClientMsgNo)
	return appendKeyString(key, clientMsgNo)
}

func encodeMessageClientMsgNoIndexKey(channelKey channel.ChannelKey, clientMsgNo string, seq uint64) []byte {
	key := encodeMessageClientMsgNoIndexPrefix(channelKey, clientMsgNo)
	return binary.BigEndian.AppendUint64(key, seq)
}

func decodeMessageClientMsgNoIndexSeq(key []byte, channelKey channel.ChannelKey, clientMsgNo string) (uint64, error) {
	prefix := encodeMessageClientMsgNoIndexPrefix(channelKey, clientMsgNo)
	if len(key) != len(prefix)+8 || !bytes.Equal(key[:len(prefix)], prefix) {
		return 0, channel.ErrCorruptValue
	}
	return binary.BigEndian.Uint64(key[len(prefix):]), nil
}

func encodeMessageIdempotencyIndexKey(channelKey channel.ChannelKey, fromUID, clientMsgNo string) []byte {
	key := encodeTableIndexPrefix(channelKey, TableIDMessage, messageIndexIDFromUIDClientMsgNo)
	key = appendKeyString(key, fromUID)
	return appendKeyString(key, clientMsgNo)
}

func (r messageRow) compatibilityEncodedRecordSize() (int, error) {
	record, err := r.toCompatibilityRecord()
	if err != nil {
		return 0, err
	}
	return record.SizeBytes, nil
}

// consumeForwardMessageFamily validates the expected primary -> payload family order.
func consumeForwardMessageFamily(seq uint64, familyID uint16, skipPayloadSeq *uint64) (bool, error) {
	switch familyID {
	case messagePrimaryFamilyID:
		if skipPayloadSeq != nil && *skipPayloadSeq != 0 {
			return false, channel.ErrCorruptState
		}
		return true, nil
	case messagePayloadFamilyID:
		if skipPayloadSeq == nil || *skipPayloadSeq != seq {
			return false, channel.ErrCorruptState
		}
		*skipPayloadSeq = 0
		return false, nil
	default:
		return false, channel.ErrCorruptState
	}
}

// consumeReverseMessageFamily validates the expected payload -> primary order during reverse scans.
func consumeReverseMessageFamily(seq uint64, familyID uint16, pendingPayloadSeq *uint64) (bool, error) {
	switch familyID {
	case messagePayloadFamilyID:
		if pendingPayloadSeq == nil || *pendingPayloadSeq != 0 {
			return false, channel.ErrCorruptState
		}
		*pendingPayloadSeq = seq
		return false, nil
	case messagePrimaryFamilyID:
		if pendingPayloadSeq != nil {
			if *pendingPayloadSeq != 0 && *pendingPayloadSeq != seq {
				return false, channel.ErrCorruptState
			}
			*pendingPayloadSeq = 0
		}
		return true, nil
	default:
		return false, channel.ErrCorruptState
	}
}
