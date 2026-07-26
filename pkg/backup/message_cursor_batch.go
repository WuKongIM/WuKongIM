package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	// MessageCursorBatchFormat identifies one immutable message cursor sidecar.
	MessageCursorBatchFormat = "wukongim-backup-message-cursor"
	// MessageCursorBatchVersion is the current cursor sidecar schema.
	MessageCursorBatchVersion uint16 = 1

	maxMessageCursorBatchHeaderBytes = 64 << 10
)

var messageCursorBatchMagic = [4]byte{'W', 'K', 'M', 'C'}

// MessageCursorBatch is one small immutable Channel-cursor delta. It is stored
// separately from message payload so restart never decrypts historical messages.
type MessageCursorBatch struct {
	HashSlot uint16
	// Generation and Sequence identify the matching message segment.
	Generation string
	Sequence   uint64
	// Checkpoint means Boundaries is a complete current Channel index and the
	// cursor chain intentionally terminates at this batch.
	Checkpoint bool
	// Previous links only cursor sidecars, never message payload segments.
	Previous *SegmentReference
	// FromCursor and NextCursor fence source reconciliation continuity.
	FromCursor string
	NextCursor string
	// SourceHighWatermark and WatermarkAtUnixMillis describe the represented cut.
	SourceHighWatermark   uint64
	WatermarkAtUnixMillis int64
	// Boundaries contains the latest cursor updates carried by the matching segment.
	Boundaries []ChannelBoundary
}

type messageCursorBatchHeader struct {
	Format                string            `json:"format"`
	Version               uint16            `json:"version"`
	HashSlot              uint16            `json:"hash_slot"`
	Generation            string            `json:"generation"`
	Sequence              uint64            `json:"sequence"`
	Checkpoint            bool              `json:"checkpoint,omitempty"`
	Previous              *SegmentReference `json:"previous,omitempty"`
	FromCursor            string            `json:"from_cursor,omitempty"`
	NextCursor            string            `json:"next_cursor"`
	SourceHighWatermark   uint64            `json:"source_high_watermark"`
	WatermarkAtUnixMillis int64             `json:"watermark_at_unix_millis"`
	IndexBytes            uint64            `json:"index_bytes"`
	IndexSHA256           string            `json:"index_sha256"`
}

// MarshalMessageCursorBatch encodes one strict cursor-only sidecar.
func MarshalMessageCursorBatch(batch MessageCursorBatch) ([]byte, error) {
	if err := validateMessageCursorBatch(batch); err != nil {
		return nil, err
	}
	index, err := MarshalChannelIndex(batch.HashSlot, batch.Boundaries)
	if err != nil {
		return nil, fmt.Errorf("%w: message cursor index: %v", ErrInvalidObject, err)
	}
	digest := sha256.Sum256(index)
	header := messageCursorBatchHeader{
		Format: MessageCursorBatchFormat, Version: MessageCursorBatchVersion,
		HashSlot: batch.HashSlot, Generation: batch.Generation, Sequence: batch.Sequence,
		Checkpoint: batch.Checkpoint,
		Previous:   cloneSegmentReference(batch.Previous),
		FromCursor: batch.FromCursor, NextCursor: batch.NextCursor,
		SourceHighWatermark:   batch.SourceHighWatermark,
		WatermarkAtUnixMillis: batch.WatermarkAtUnixMillis,
		IndexBytes:            uint64(len(index)), IndexSHA256: hex.EncodeToString(digest[:]),
	}
	headerBody, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("marshal message cursor header: %w", err)
	}
	if len(headerBody) > maxMessageCursorBatchHeaderBytes ||
		len(index) > maxObjectPlaintextBytes-16-len(headerBody) {
		return nil, fmt.Errorf("%w: message cursor batch exceeds size limit", ErrInvalidObject)
	}
	body := make([]byte, 0, 16+len(headerBody)+len(index))
	body = append(body, messageCursorBatchMagic[:]...)
	body = binary.BigEndian.AppendUint32(body, uint32(len(headerBody)))
	body = binary.BigEndian.AppendUint64(body, uint64(len(index)))
	body = append(body, headerBody...)
	body = append(body, index...)
	return body, nil
}

// LoadMessageCursorBatch strictly decodes and verifies one cursor-only sidecar.
func LoadMessageCursorBatch(body []byte) (MessageCursorBatch, error) {
	const prefixBytes = 4 + 4 + 8
	if len(body) < prefixBytes || len(body) > maxObjectPlaintextBytes ||
		!bytes.Equal(body[:4], messageCursorBatchMagic[:]) {
		return MessageCursorBatch{}, fmt.Errorf("%w: message cursor batch header is invalid", ErrInvalidObject)
	}
	headerBytes := uint64(binary.BigEndian.Uint32(body[4:8]))
	indexBytes := binary.BigEndian.Uint64(body[8:16])
	if headerBytes == 0 || headerBytes > maxMessageCursorBatchHeaderBytes ||
		headerBytes > math.MaxInt || indexBytes > math.MaxInt ||
		headerBytes+indexBytes != uint64(len(body)-prefixBytes) {
		return MessageCursorBatch{}, fmt.Errorf("%w: message cursor batch lengths are invalid", ErrInvalidObject)
	}
	headerEnd := prefixBytes + int(headerBytes)
	decoder := json.NewDecoder(bytes.NewReader(body[prefixBytes:headerEnd]))
	decoder.DisallowUnknownFields()
	var header messageCursorBatchHeader
	if err := decoder.Decode(&header); err != nil {
		return MessageCursorBatch{}, fmt.Errorf("%w: decode message cursor header: %v", ErrInvalidObject, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return MessageCursorBatch{}, fmt.Errorf("%w: trailing message cursor header data", ErrInvalidObject)
	}
	index := body[headerEnd:]
	digest := sha256.Sum256(index)
	if header.Format != MessageCursorBatchFormat ||
		header.Version != MessageCursorBatchVersion ||
		header.IndexBytes != indexBytes ||
		header.IndexSHA256 != hex.EncodeToString(digest[:]) {
		return MessageCursorBatch{}, fmt.Errorf("%w: message cursor batch checksum mismatch", ErrObjectCorrupt)
	}
	hashSlot, boundaries, err := LoadChannelIndex(index)
	if err != nil || hashSlot != header.HashSlot {
		return MessageCursorBatch{}, fmt.Errorf("%w: message cursor index is invalid", ErrInvalidObject)
	}
	batch := MessageCursorBatch{
		HashSlot: header.HashSlot, Generation: header.Generation, Sequence: header.Sequence,
		Checkpoint: header.Checkpoint,
		Previous:   cloneSegmentReference(header.Previous),
		FromCursor: header.FromCursor, NextCursor: header.NextCursor,
		SourceHighWatermark:   header.SourceHighWatermark,
		WatermarkAtUnixMillis: header.WatermarkAtUnixMillis,
		Boundaries:            boundaries,
	}
	if err := validateMessageCursorBatch(batch); err != nil {
		return MessageCursorBatch{}, err
	}
	return batch, nil
}

func validateMessageCursorBatch(batch MessageCursorBatch) error {
	if err := validateRestorePointID(batch.Generation); err != nil {
		return fmt.Errorf("%w: message cursor generation: %v", ErrInvalidObject, err)
	}
	if batch.Sequence == 0 || batch.SourceHighWatermark == 0 ||
		batch.WatermarkAtUnixMillis <= 0 ||
		(len(batch.Boundaries) == 0 && !batch.Checkpoint) {
		return fmt.Errorf("%w: message cursor batch boundary is incomplete", ErrInvalidObject)
	}
	if err := validateSegmentBatchCursor(batch.FromCursor); err != nil {
		return err
	}
	if err := validateSegmentBatchCursor(batch.NextCursor); err != nil ||
		batch.NextCursor == batch.FromCursor {
		return fmt.Errorf("%w: message cursor batch next cursor is invalid", ErrInvalidObject)
	}
	if batch.Checkpoint {
		if batch.Previous != nil {
			return fmt.Errorf("%w: message cursor checkpoint has predecessor", ErrInvalidObject)
		}
	} else if batch.Sequence == 1 {
		if batch.Previous != nil {
			return fmt.Errorf("%w: first message cursor batch has predecessor", ErrInvalidObject)
		}
	} else if batch.Previous == nil {
		return fmt.Errorf("%w: message cursor predecessor is required", ErrInvalidObject)
	}
	if batch.Previous != nil {
		if err := validateSegmentReference(*batch.Previous); err != nil {
			return err
		}
	}
	return nil
}
