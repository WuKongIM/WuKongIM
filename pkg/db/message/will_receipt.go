package message

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

// WillReceipt retains immutable publication identity independently of ordinary
// history. A local materialization is not current Channel authority or execution
// permission; callers must establish the appropriate cluster read barrier.
type WillReceipt struct {
	MessageSeq, MessageID uint64
	// ServerTimestampMS is the first source append time, never a retry clock.
	ServerTimestampMS int64
	// ContentHash binds the sender, client number, body and complete metadata.
	ContentHash [32]byte
}

// WillPublicationHash computes the version-1 receipt's length-delimited content
// identity. A client message number cannot select the server Will domain.
func WillPublicationHash(uid, client string, payload, metadata []byte) ([32]byte, error) {
	var out [32]byte
	md, err := publication.Decode(metadata)
	if err != nil || md.Source != publication.SourceWill || md.ServerWillKey == "" || uid == "" || len(uid) > 65535 || client == "" || len(client) > 65535 {
		return out, dberrors.ErrInvalidArgument
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("wk-will-receipt-v1\x00"))
	var size [8]byte
	for _, field := range [][]byte{[]byte(uid), []byte(client), payload, metadata} {
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(field)
	}
	copy(out[:], hash.Sum(nil))
	return out, nil
}

func willReceiptPrefix(key ChannelKey) []byte {
	return encodeMessageSystemPrefix(key, messageSystemIDWillReceipt)
}
func willReceiptKey(key ChannelKey, identity IdempotencyKey) []byte {
	return keycodec.AppendString(keycodec.AppendString(willReceiptPrefix(key), identity.ServerWillKey), identity.FromUID)
}
func willReceiptIdentity(channel ChannelKey, key []byte) (IdempotencyKey, bool) {
	prefix := willReceiptPrefix(channel)
	var id IdempotencyKey
	if !bytes.HasPrefix(key, prefix) {
		return id, false
	}
	rest := key[len(prefix):]
	read := func() (string, bool) {
		if len(rest) < 2 {
			return "", false
		}
		n := int(binary.BigEndian.Uint16(rest))
		rest = rest[2:]
		if n > len(rest) {
			return "", false
		}
		s := string(rest[:n])
		rest = rest[n:]
		return s, true
	}
	var ok bool
	id.ServerWillKey, ok = read()
	if !ok {
		return id, false
	}
	id.FromUID, ok = read()
	return id, ok && len(rest) == 0 && id.valid() && id.ServerWillKey != ""
}
func (r WillReceipt) valid() bool {
	return r.MessageSeq > 0 && r.MessageID > 0 && r.ServerTimestampMS > 0 && r.ContentHash != ([32]byte{})
}
func encodeWillReceipt(key []byte, r WillReceipt) ([]byte, error) {
	if !r.valid() {
		return nil, dberrors.ErrInvalidArgument
	}
	b := make([]byte, 56)
	binary.BigEndian.PutUint64(b, r.MessageSeq)
	binary.BigEndian.PutUint64(b[8:], r.MessageID)
	binary.BigEndian.PutUint64(b[16:], uint64(r.ServerTimestampMS))
	copy(b[24:], r.ContentHash[:])
	return rowcodec.Wrap(key, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, b), nil
}
func decodeWillReceipt(channel ChannelKey, key, value []byte) (WillReceipt, error) {
	var r WillReceipt
	if _, ok := willReceiptIdentity(channel, key); !ok || len(value) != rowcodec.EnvelopeLen(56) {
		return r, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return r, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecFixed || env.Flags != rowcodec.FlagChecksum || len(env.Payload) != 56 {
		return r, dberrors.ErrCorruptValue
	}
	r.MessageSeq = binary.BigEndian.Uint64(env.Payload)
	r.MessageID = binary.BigEndian.Uint64(env.Payload[8:])
	r.ServerTimestampMS = int64(binary.BigEndian.Uint64(env.Payload[16:]))
	copy(r.ContentHash[:], env.Payload[24:])
	if !r.valid() {
		return WillReceipt{}, dberrors.ErrCorruptValue
	}
	return r, nil
}
func willReceiptFromRow(row messageRow) (WillReceipt, error) {
	hash, err := WillPublicationHash(row.FromUID, row.ClientMsgNo, row.Payload, row.PublicationMetadata)
	if err != nil {
		return WillReceipt{}, err
	}
	r := WillReceipt{MessageSeq: row.MessageSeq, MessageID: row.MessageID, ServerTimestampMS: row.ServerTimestampMS, ContentHash: hash}
	if !r.valid() {
		return WillReceipt{}, dberrors.ErrInvalidArgument
	}
	return r, nil
}
func loadWillReceipt(view messageBackupReadView, channel ChannelKey, id IdempotencyKey) (WillReceipt, bool, error) {
	key := willReceiptKey(channel, id)
	value, found, err := view.Get(key)
	if err != nil || !found {
		return WillReceipt{}, found, err
	}
	r, err := decodeWillReceipt(channel, key, value)
	return r, err == nil, err
}
func (l *channelEntry) stageWillReceipt(batch *engine.Batch, row messageRow, id IdempotencyKey) error {
	receipt, err := willReceiptFromRow(row)
	if err != nil {
		return err
	}
	key := willReceiptKey(l.key, id)
	value, err := encodeWillReceipt(key, receipt)
	if err != nil {
		return err
	}
	return batch.Set(key, value)
}

// LookupWillReceipt pins local committed evidence, including the physical trim
// witness when the original is gone. Missing data without that witness is never
// silently converted into an absent publication. False grants no retry authority.
func (l *ChannelLog) LookupWillReceipt(ctx context.Context, id IdempotencyKey) (WillReceipt, bool, error) {
	if !id.valid() || id.ServerWillKey == "" || len(id.FromUID) > 65535 {
		return WillReceipt{}, false, dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return WillReceipt{}, false, err
	}
	defer l.endUse()
	if err := ctx.Err(); err != nil {
		return WillReceipt{}, false, err
	}
	view, err := l.db.engine.NewSnapshot()
	if err != nil {
		return WillReceipt{}, false, err
	}
	defer view.Close()
	receipt, found, err := loadWillReceipt(view, l.key, id)
	if err != nil {
		return WillReceipt{}, false, err
	}
	if !found {
		_, indexed, e := view.Get(l.idempotencyStorageKey(id))
		if e != nil {
			return WillReceipt{}, false, e
		}
		if indexed {
			return WillReceipt{}, false, dberrors.ErrCorruptState
		}
		return WillReceipt{}, false, nil
	}
	checkpoint, exists, err := view.Get(encodeCheckpointKey(l.key))
	if err != nil {
		return WillReceipt{}, false, err
	}
	if !exists {
		return WillReceipt{}, false, nil
	}
	cp, err := decodeCheckpoint(checkpoint)
	if err != nil || validateCheckpoint(cp) != nil {
		return WillReceipt{}, false, dberrors.ErrCorruptState
	}
	if receipt.MessageSeq > cp.HW {
		return WillReceipt{}, false, nil
	}
	if err = validateWillReceiptOriginal(view, l.key, id, receipt); err != nil {
		return WillReceipt{}, false, err
	}
	if err = ctx.Err(); err != nil {
		return WillReceipt{}, false, err
	}
	return receipt, true, nil
}
func validateWillReceiptOriginal(view messageBackupReadView, channel ChannelKey, id IdempotencyKey, r WillReceipt) error {
	key := encodeMessageRowKey(channel, r.MessageSeq, messageHeaderFamilyID)
	value, found, err := view.Get(key)
	if err != nil {
		return err
	}
	if !found {
		retention, present, err := snapshotRetentionState(view, channel)
		if err != nil {
			return err
		}
		if !present || retention.PhysicalRetentionThroughSeq < r.MessageSeq {
			return dberrors.ErrCorruptState
		}
		return nil
	}
	row := messageRow{MessageSeq: r.MessageSeq}
	if err = decodeMessageHeader(key, value, &row); err != nil {
		return err
	}
	identity, err := rowIdempotencyKey(row.FromUID, row.ClientMsgNo, row.PublicationMetadata)
	if err != nil || identity != id {
		return dberrors.ErrCorruptState
	}
	actual, err := willReceiptFromRow(row)
	if err != nil || actual != r {
		return dberrors.ErrCorruptState
	}
	return nil
}

// Trimming can materialize a legacy keyed Will from its still-present original.
// Receipt insertion and body deletion share the same commit; existing proof is
// compared before deletion so corruption cannot be hidden by physical cleanup.
func (l *ChannelLog) stageRetainedWillReceipt(batch *engine.Batch, msg Message, id IdempotencyKey) error {
	row := messageRow{MessageSeq: msg.MessageSeq, MessageID: msg.MessageID, ServerTimestampMS: msg.ServerTimestampMS,
		FromUID: msg.FromUID, ClientMsgNo: msg.ClientMsgNo, Payload: msg.Payload, PublicationMetadata: msg.PublicationMetadata}
	expected, err := willReceiptFromRow(row)
	if err != nil {
		return err
	}
	old, found, err := loadWillReceipt(l.db.engine, l.key, id)
	if err != nil {
		return err
	}
	if found {
		if old != expected {
			return dberrors.ErrCorruptState
		}
		return nil
	}
	return l.stageWillReceipt(batch, row, id)
}
