package message

import (
	"context"
	"encoding/binary"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
)

// MQTTSourceState is a replica's materialized protection decision for one log
// incarnation. It is not a lease, permission, quorum proof or named cursor.
type MQTTSourceState struct {
	// Generation follows the log incarnation, not a leader or process lifetime.
	Generation string
	// Revision orders replicated decisions; local reads do not allocate it.
	Revision uint64
	// StartAfter is the immutable subscription/source activation boundary.
	StartAfter uint64
	// CopiedThrough may release source rows only after the caller has verified
	// replicated shared content. The database cannot establish that proof.
	CopiedThrough uint64
	// ReceiptDigest binds the externally verified copy receipt, not just a time.
	ReceiptDigest [32]byte
}

func (s MQTTSourceState) valid() bool {
	if s.Revision == 0 || len(s.Generation) > 128 || strings.TrimSpace(s.Generation) == "" ||
		!utf8.ValidString(s.Generation) || strings.ContainsRune(s.Generation, 0) || s.CopiedThrough < s.StartAfter {
		return false
	}
	initial := s.CopiedThrough == s.StartAfter
	return initial == (s.ReceiptDigest == [32]byte{}) && initial == (s.Revision == 1)
}

func mqttSourceKey(key ChannelKey) []byte {
	return encodeMessageSystemPrefix(key, messageSystemIDMQTTSource)
}

func encodeMQTTSourceState(key []byte, s MQTTSourceState) []byte {
	b := make([]byte, 0, 58+len(s.Generation))
	b = binary.BigEndian.AppendUint16(b, uint16(len(s.Generation)))
	b = append(b, s.Generation...)
	b = binary.BigEndian.AppendUint64(b, s.Revision)
	b = binary.BigEndian.AppendUint64(b, s.StartAfter)
	b = binary.BigEndian.AppendUint64(b, s.CopiedThrough)
	b = append(b, s.ReceiptDigest[:]...)
	return rowcodec.Wrap(key, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, b)
}

func decodeMQTTSourceState(key, value []byte) (MQTTSourceState, error) {
	if len(value) > rowcodec.EnvelopeLen(58+128) {
		return MQTTSourceState{}, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return MQTTSourceState{}, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecFixed || env.Flags != rowcodec.FlagChecksum || len(env.Payload) < 58 {
		return MQTTSourceState{}, dberrors.ErrCorruptValue
	}
	n := int(binary.BigEndian.Uint16(env.Payload))
	if n > 128 || len(env.Payload) != 58+n {
		return MQTTSourceState{}, dberrors.ErrCorruptValue
	}
	b := env.Payload[2+n:]
	s := MQTTSourceState{Generation: string(env.Payload[2 : 2+n]), Revision: binary.BigEndian.Uint64(b),
		StartAfter: binary.BigEndian.Uint64(b[8:]), CopiedThrough: binary.BigEndian.Uint64(b[16:])}
	copy(s.ReceiptDigest[:], b[24:])
	if !s.valid() {
		return MQTTSourceState{}, dberrors.ErrCorruptValue
	}
	return s, nil
}

// LoadMQTTSourceState returns the durable replica state without claiming fresh
// distributed authority. Callers must establish that authority independently.
func (l *ChannelLog) LoadMQTTSourceState(ctx context.Context) (MQTTSourceState, bool, error) {
	if err := l.beginUse(); err != nil {
		return MQTTSourceState{}, false, err
	}
	defer l.endUse()
	return l.channelEntry.loadMQTTSourceState(ctx)
}

func (e *channelEntry) loadMQTTSourceState(ctx context.Context) (MQTTSourceState, bool, error) {
	if err := ctx.Err(); err != nil {
		return MQTTSourceState{}, false, err
	}
	key := mqttSourceKey(e.key)
	value, ok, err := e.db.engine.Get(key)
	if err != nil || !ok {
		return MQTTSourceState{}, ok, err
	}
	s, err := decodeMQTTSourceState(key, value)
	return s, err == nil, err
}

// ApplyMQTTSourceState materializes one externally replicated CAS decision.
// Before advancing, the caller MUST prove current authority and shared-copy
// durability; a local commit here cannot authorize SUBACK or source release on
// another replica. No product runtime calls this primitive before that protocol.
func (l *ChannelLog) ApplyMQTTSourceState(ctx context.Context, expectedRevision uint64, next MQTTSourceState) error {
	if !next.valid() || expectedRevision == math.MaxUint64 || next.Revision != expectedRevision+1 {
		return dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return err
	}
	defer l.endUse()
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	l.checkpointMu.Lock()
	defer l.checkpointMu.Unlock()
	current, present, err := l.channelEntry.loadMQTTSourceState(ctx)
	if err != nil {
		return err
	}
	exactRetry := present && current == next
	if present && !exactRetry {
		if current.Revision != expectedRevision || next.Generation != current.Generation || next.StartAfter != current.StartAfter || next.CopiedThrough <= current.CopiedThrough {
			return dberrors.ErrConflict
		}
	} else if !present && (expectedRevision != 0 || next.CopiedThrough != next.StartAfter) {
		return dberrors.ErrConflict
	}
	cp, checkpointPresent, err := l.loadCheckpoint(ctx)
	if err != nil {
		return err
	}
	if present && !checkpointPresent {
		return dberrors.ErrCorruptState
	}
	if next.CopiedThrough > cp.HW {
		return dberrors.ErrConflict
	}
	retention, _, err := l.loadRetentionState(ctx)
	if err != nil {
		return err
	}
	if present && retention.PhysicalRetentionThroughSeq > current.CopiedThrough {
		return dberrors.ErrCorruptState
	}
	if !present && retention.PhysicalRetentionThroughSeq > next.StartAfter {
		return dberrors.ErrConflict
	}
	if exactRetry {
		return nil
	}
	key := mqttSourceKey(l.key)
	batch := l.db.engine.NewBatch()
	defer batch.Close()
	if !checkpointPresent {
		// Distinguish an explicitly empty log from a lost checkpoint on every
		// later retry/read/truncate. The checkpoint mutex prevents regression.
		if err := batch.Set(encodeCheckpointKey(l.key), encodeCheckpoint(Checkpoint{})); err != nil {
			return err
		}
	}
	if err := batch.Set(key, encodeMQTTSourceState(key, next)); err != nil {
		return err
	}
	if err := l.stageCatalog(batch); err != nil {
		return err
	}
	return batch.Commit(true)
}

// ReadMQTTProtectedSource reads an uncopied, committed original range despite
// ordinary logical retention. It never applies edit overlays or skips gaps.
// The caller still owns the current Channel authority/replication barrier.
func (l *ChannelLog) ReadMQTTProtectedSource(ctx context.Context, generation string, from, through uint64, opts ReadOptions) ([]Message, error) {
	if from == 0 || through < from || opts.Limit <= 0 || opts.Limit > 256 || opts.MaxBytes <= 0 || opts.MaxBytes > 16<<20 {
		return nil, dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return nil, err
	}
	defer l.endUse()
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	state, present, err := l.channelEntry.loadMQTTSourceState(ctx)
	if err != nil {
		return nil, err
	}
	if !present || state.Generation != generation || from <= state.CopiedThrough {
		return nil, dberrors.ErrConflict
	}
	cp, checkpointPresent, err := l.loadCheckpoint(ctx)
	if err != nil {
		return nil, err
	}
	if !checkpointPresent {
		return nil, dberrors.ErrCorruptState
	}
	if through > cp.HW {
		return nil, dberrors.ErrConflict
	}
	result := make([]Message, 0, opts.Limit)
	bytesRead := 0
	for seq := from; ; seq++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, found, err := l.getRowBySeq(ctx, seq)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, dberrors.ErrCorruptState
		}
		n := len(row.Payload) + len(row.PublicationMetadata)
		if n > opts.MaxBytes-bytesRead {
			if len(result) == 0 {
				return nil, dberrors.ErrInvalidArgument
			}
			break
		}
		result = append(result, messageFromRow(row))
		bytesRead += n
		if seq == through || len(result) == opts.Limit || bytesRead == opts.MaxBytes {
			break
		}
	}
	return result, nil
}

// validateMQTTSourceTruncation keeps a source's committed positions intact even
// when a legacy suffix-truncate caller lacks an exact proposal manifest.
func (e *channelEntry) validateMQTTSourceTruncation(ctx context.Context, to uint64) error {
	_, present, err := e.loadMQTTSourceState(ctx)
	if err != nil || !present {
		return err
	}
	value, ok, err := e.db.engine.Get(encodeCheckpointKey(e.key))
	if err != nil {
		return err
	}
	if !ok {
		return dberrors.ErrCorruptState
	}
	cp, err := decodeCheckpoint(value)
	if err != nil {
		return err
	}
	if to < cp.HW {
		return dberrors.ErrConflict
	}
	return nil
}
