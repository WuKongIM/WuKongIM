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
	"unicode/utf8"
)

const (
	// SegmentBatchFormat identifies the portable plaintext carried by a segment.
	SegmentBatchFormat = "wukongim-backup-segment-batch"
	// SegmentBatchVersion is the current portable batch schema version.
	SegmentBatchVersion uint32 = 1

	maxSegmentBatchHeaderBytes = 64 << 10
	maxSegmentBatchCursorBytes = 8 << 10
)

var segmentBatchMagic = [4]byte{'W', 'K', 'S', 'B'}

// SegmentBatch contains bounded source records and their durable scan evidence.
type SegmentBatch struct {
	// HashSlot identifies the logical source partition.
	HashSlot uint16
	// Stream identifies whether Records contain metadata or messages.
	Stream SegmentStream
	// Generation identifies the independently replaceable Slot generation.
	Generation string
	// Sequence orders batches within Generation and Stream.
	Sequence uint64
	// Previous links to the prior committed segment in this stream.
	Previous *SegmentReference
	// FromCursor and NextCursor bind the authoritative paged source interval.
	FromCursor string
	NextCursor string
	// SourceHighWatermark is the authoritative position this scan reconciles.
	SourceHighWatermark uint64
	// WatermarkAtUnixMillis is the UTC time represented by SourceHighWatermark.
	WatermarkAtUnixMillis int64
	// Records are portable, length-delimited committed source records.
	Records [][]byte
	// MessageCursors are the bounded Channel cursor updates carried by a message batch.
	MessageCursors []ChannelBoundary
}

// SegmentBatchInfo is the bounded authenticated envelope returned by streaming
// inspection without retaining record or Channel-boundary collections.
type SegmentBatchInfo struct {
	// HashSlot, Stream, Generation, and Sequence identify the logical segment.
	HashSlot   uint16
	Stream     SegmentStream
	Generation string
	Sequence   uint64
	// Previous authenticates the preceding segment in this stream.
	Previous *SegmentReference
	// SourceHighWatermark and WatermarkAtUnixMillis identify the source cut.
	SourceHighWatermark   uint64
	WatermarkAtUnixMillis int64
	// RecordCount and MessageCursorCount are verified typed collection sizes.
	RecordCount        uint64
	MessageCursorCount uint32
}

type segmentBatchHeader struct {
	Format                string            `json:"format"`
	Version               uint32            `json:"version"`
	HashSlot              uint16            `json:"hash_slot"`
	Stream                SegmentStream     `json:"stream"`
	Generation            string            `json:"generation"`
	Sequence              uint64            `json:"sequence"`
	Previous              *SegmentReference `json:"previous,omitempty"`
	FromCursor            string            `json:"from_cursor"`
	NextCursor            string            `json:"next_cursor"`
	SourceHighWatermark   uint64            `json:"source_high_watermark"`
	WatermarkAtUnixMillis int64             `json:"watermark_at_unix_millis"`
	RecordCount           uint64            `json:"record_count"`
	RecordsBytes          uint64            `json:"records_bytes"`
	RecordsSHA256         string            `json:"records_sha256"`
	MessageCursorBytes    uint64            `json:"message_cursor_bytes"`
	MessageCursorSHA256   string            `json:"message_cursor_sha256"`
}

// MarshalSegmentBatch encodes one strict, bounded segment plaintext.
func MarshalSegmentBatch(batch SegmentBatch) ([]byte, error) {
	if err := validateSegmentBatch(batch); err != nil {
		return nil, err
	}
	records, err := marshalSegmentBatchRecords(batch.Records)
	if err != nil {
		return nil, err
	}
	var cursors []byte
	if batch.Stream == SegmentStreamMessages {
		cursors, err = MarshalChannelIndex(batch.HashSlot, batch.MessageCursors)
		if err != nil {
			return nil, fmt.Errorf("%w: segment message cursor index: %v", ErrInvalidObject, err)
		}
	}
	recordHash := sha256.Sum256(records)
	cursorHash := sha256.Sum256(cursors)
	header := segmentBatchHeader{
		Format: SegmentBatchFormat, Version: SegmentBatchVersion,
		HashSlot: batch.HashSlot, Stream: batch.Stream, Generation: batch.Generation,
		Sequence: batch.Sequence, Previous: cloneSegmentReference(batch.Previous),
		FromCursor: batch.FromCursor, NextCursor: batch.NextCursor,
		SourceHighWatermark: batch.SourceHighWatermark, WatermarkAtUnixMillis: batch.WatermarkAtUnixMillis,
		RecordCount: uint64(len(batch.Records)), RecordsBytes: uint64(len(records)),
		RecordsSHA256:      hex.EncodeToString(recordHash[:]),
		MessageCursorBytes: uint64(len(cursors)), MessageCursorSHA256: hex.EncodeToString(cursorHash[:]),
	}
	headerBody, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("marshal segment batch header: %w", err)
	}
	if len(headerBody) > maxSegmentBatchHeaderBytes {
		return nil, fmt.Errorf("%w: segment batch header exceeds size limit", ErrInvalidObject)
	}
	total := 4 + 4 + 8 + 8 + len(headerBody) + len(cursors) + len(records)
	if total > maxObjectPlaintextBytes {
		return nil, fmt.Errorf("%w: segment batch exceeds size limit", ErrInvalidObject)
	}
	body := make([]byte, 0, total)
	body = append(body, segmentBatchMagic[:]...)
	body = binary.BigEndian.AppendUint32(body, uint32(len(headerBody)))
	body = binary.BigEndian.AppendUint64(body, uint64(len(cursors)))
	body = binary.BigEndian.AppendUint64(body, uint64(len(records)))
	body = append(body, headerBody...)
	body = append(body, cursors...)
	body = append(body, records...)
	return body, nil
}

// LoadSegmentBatch strictly decodes and verifies one portable segment plaintext.
func LoadSegmentBatch(body []byte) (SegmentBatch, error) {
	const prefixBytes = 4 + 4 + 8 + 8
	if len(body) < prefixBytes || len(body) > maxObjectPlaintextBytes {
		return SegmentBatch{}, fmt.Errorf("%w: segment batch size is outside bounds", ErrInvalidObject)
	}
	if !bytes.Equal(body[:4], segmentBatchMagic[:]) {
		return SegmentBatch{}, fmt.Errorf("%w: segment batch magic is invalid", ErrInvalidObject)
	}
	headerBytes := uint64(binary.BigEndian.Uint32(body[4:8]))
	cursorBytes := binary.BigEndian.Uint64(body[8:16])
	recordBytes := binary.BigEndian.Uint64(body[16:24])
	if headerBytes == 0 || headerBytes > maxSegmentBatchHeaderBytes ||
		headerBytes > math.MaxInt || cursorBytes > math.MaxInt || recordBytes > math.MaxInt ||
		headerBytes+cursorBytes+recordBytes != uint64(len(body)-prefixBytes) {
		return SegmentBatch{}, fmt.Errorf("%w: segment batch lengths are invalid", ErrInvalidObject)
	}
	headerEnd := prefixBytes + int(headerBytes)
	cursorEnd := headerEnd + int(cursorBytes)
	headerBody := body[prefixBytes:headerEnd]
	cursorBody := body[headerEnd:cursorEnd]
	recordBody := body[cursorEnd:]

	decoder := json.NewDecoder(bytes.NewReader(headerBody))
	decoder.DisallowUnknownFields()
	var header segmentBatchHeader
	if err := decoder.Decode(&header); err != nil {
		return SegmentBatch{}, fmt.Errorf("%w: decode segment batch header: %v", ErrInvalidObject, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SegmentBatch{}, fmt.Errorf("%w: trailing segment batch header data", ErrInvalidObject)
	}
	if header.Format != SegmentBatchFormat || header.Version != SegmentBatchVersion ||
		header.RecordsBytes != recordBytes || header.MessageCursorBytes != cursorBytes {
		return SegmentBatch{}, fmt.Errorf("%w: segment batch header is inconsistent", ErrInvalidObject)
	}
	recordHash := sha256.Sum256(recordBody)
	cursorHash := sha256.Sum256(cursorBody)
	if header.RecordsSHA256 != hex.EncodeToString(recordHash[:]) ||
		header.MessageCursorSHA256 != hex.EncodeToString(cursorHash[:]) {
		return SegmentBatch{}, fmt.Errorf("%w: segment batch checksum mismatch", ErrObjectCorrupt)
	}
	records, err := loadSegmentBatchRecords(recordBody, header.RecordCount)
	if err != nil {
		return SegmentBatch{}, err
	}
	var cursors []ChannelBoundary
	if header.Stream == SegmentStreamMessages {
		cursorSlot, decoded, err := LoadChannelIndex(cursorBody)
		if err != nil || cursorSlot != header.HashSlot || len(decoded) == 0 {
			return SegmentBatch{}, fmt.Errorf("%w: segment message cursor index is invalid", ErrInvalidObject)
		}
		cursors = decoded
	} else if len(cursorBody) != 0 {
		return SegmentBatch{}, fmt.Errorf("%w: metadata segment contains message cursors", ErrInvalidObject)
	}
	batch := SegmentBatch{
		HashSlot: header.HashSlot, Stream: header.Stream, Generation: header.Generation,
		Sequence: header.Sequence, Previous: cloneSegmentReference(header.Previous),
		FromCursor: header.FromCursor, NextCursor: header.NextCursor,
		SourceHighWatermark: header.SourceHighWatermark, WatermarkAtUnixMillis: header.WatermarkAtUnixMillis,
		Records: records, MessageCursors: cursors,
	}
	if err := validateSegmentBatch(batch); err != nil {
		return SegmentBatch{}, err
	}
	return batch, nil
}

// InspectSegmentBatch strictly validates one segment without materializing
// record or Channel-boundary slices.
func InspectSegmentBatch(body []byte) (SegmentBatchInfo, error) {
	return ReplaySegmentBatch(
		bytes.NewReader(body), int64(len(body)), nil, nil,
	)
}

// ReplaySegmentBatch strictly validates one segment and visits records and
// Channel boundaries one at a time. A visitor error stops replay immediately.
func ReplaySegmentBatch(
	reader io.Reader,
	size int64,
	visitRecord func([]byte) error,
	visitBoundary func(ChannelBoundary) error,
) (SegmentBatchInfo, error) {
	header, cursorBytes, recordBytes, err := readSegmentBatchEnvelope(reader, size)
	if err != nil {
		return SegmentBatchInfo{}, err
	}
	info := SegmentBatchInfo{
		HashSlot: header.HashSlot, Stream: header.Stream,
		Generation: header.Generation, Sequence: header.Sequence,
		Previous:              cloneSegmentReference(header.Previous),
		SourceHighWatermark:   header.SourceHighWatermark,
		WatermarkAtUnixMillis: header.WatermarkAtUnixMillis,
		RecordCount:           header.RecordCount,
	}
	cursorDigest := sha256.New()
	if cursorBytes > 0 {
		cursorReader := io.TeeReader(
			io.LimitReader(reader, int64(cursorBytes)), cursorDigest,
		)
		slot, count, err := ReplayChannelIndex(
			cursorReader, int64(cursorBytes), visitBoundary,
		)
		if err != nil || slot != header.HashSlot || count == 0 {
			return SegmentBatchInfo{}, fmt.Errorf(
				"%w: segment message cursor index is invalid",
				ErrInvalidObject,
			)
		}
		info.MessageCursorCount = count
	}
	if header.MessageCursorSHA256 !=
		hex.EncodeToString(cursorDigest.Sum(nil)) {
		return SegmentBatchInfo{}, fmt.Errorf(
			"%w: segment batch cursor checksum mismatch", ErrObjectCorrupt,
		)
	}

	if header.RecordCount == 0 ||
		header.RecordCount > recordBytes/5+1 ||
		header.RecordCount > math.MaxInt {
		return SegmentBatchInfo{}, fmt.Errorf(
			"%w: segment batch record count is invalid", ErrInvalidObject,
		)
	}
	recordLimit := &io.LimitedReader{R: reader, N: int64(recordBytes)}
	recordDigest := sha256.New()
	recordReader := io.TeeReader(recordLimit, recordDigest)
	for index := uint64(0); index < header.RecordCount; index++ {
		var recordSize uint32
		if err := binary.Read(
			recordReader, binary.BigEndian, &recordSize,
		); err != nil || recordSize == 0 ||
			uint64(recordSize) > uint64(recordLimit.N) ||
			uint64(recordSize) > maxObjectPlaintextBytes {
			return SegmentBatchInfo{}, fmt.Errorf(
				"%w: segment batch record %d is truncated",
				ErrInvalidObject, index,
			)
		}
		record := make([]byte, int(recordSize))
		if _, err := io.ReadFull(recordReader, record); err != nil {
			return SegmentBatchInfo{}, fmt.Errorf(
				"%w: segment batch record %d is truncated",
				ErrInvalidObject, index,
			)
		}
		if visitRecord != nil {
			if err := visitRecord(record); err != nil {
				return SegmentBatchInfo{}, err
			}
		}
	}
	if recordLimit.N != 0 {
		return SegmentBatchInfo{}, fmt.Errorf(
			"%w: trailing segment batch record data", ErrInvalidObject,
		)
	}
	if header.RecordsSHA256 != hex.EncodeToString(recordDigest.Sum(nil)) {
		return SegmentBatchInfo{}, fmt.Errorf(
			"%w: segment batch record checksum mismatch", ErrObjectCorrupt,
		)
	}
	return info, nil
}

func readSegmentBatchEnvelope(
	reader io.Reader,
	size int64,
) (segmentBatchHeader, uint64, uint64, error) {
	const prefixBytes = 4 + 4 + 8 + 8
	if reader == nil || size < prefixBytes || size > maxObjectPlaintextBytes {
		return segmentBatchHeader{}, 0, 0, fmt.Errorf(
			"%w: segment batch size is outside bounds", ErrInvalidObject,
		)
	}
	var prefix [prefixBytes]byte
	if _, err := io.ReadFull(reader, prefix[:]); err != nil ||
		!bytes.Equal(prefix[:4], segmentBatchMagic[:]) {
		return segmentBatchHeader{}, 0, 0, fmt.Errorf(
			"%w: segment batch magic is invalid", ErrInvalidObject,
		)
	}
	headerBytes := uint64(binary.BigEndian.Uint32(prefix[4:8]))
	cursorBytes := binary.BigEndian.Uint64(prefix[8:16])
	recordBytes := binary.BigEndian.Uint64(prefix[16:24])
	if headerBytes == 0 || headerBytes > maxSegmentBatchHeaderBytes ||
		headerBytes > math.MaxInt || cursorBytes > math.MaxInt ||
		recordBytes > math.MaxInt ||
		headerBytes+cursorBytes+recordBytes != uint64(size-prefixBytes) {
		return segmentBatchHeader{}, 0, 0, fmt.Errorf(
			"%w: segment batch lengths are invalid", ErrInvalidObject,
		)
	}
	headerBody := make([]byte, int(headerBytes))
	if _, err := io.ReadFull(reader, headerBody); err != nil {
		return segmentBatchHeader{}, 0, 0, fmt.Errorf(
			"%w: segment batch header is truncated", ErrInvalidObject,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(headerBody))
	decoder.DisallowUnknownFields()
	var header segmentBatchHeader
	if err := decoder.Decode(&header); err != nil {
		return segmentBatchHeader{}, 0, 0, fmt.Errorf(
			"%w: decode segment batch header: %v", ErrInvalidObject, err,
		)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return segmentBatchHeader{}, 0, 0, fmt.Errorf(
			"%w: trailing segment batch header data", ErrInvalidObject,
		)
	}
	if header.Format != SegmentBatchFormat ||
		header.Version != SegmentBatchVersion ||
		header.RecordsBytes != recordBytes ||
		header.MessageCursorBytes != cursorBytes ||
		(header.Stream != SegmentStreamMetadata &&
			header.Stream != SegmentStreamMessages) ||
		validateRestorePointID(header.Generation) != nil ||
		header.Sequence == 0 || header.SourceHighWatermark == 0 ||
		header.WatermarkAtUnixMillis <= 0 ||
		validateSegmentBatchCursor(header.FromCursor) != nil ||
		validateSegmentBatchCursor(header.NextCursor) != nil ||
		header.NextCursor == header.FromCursor ||
		(header.Sequence == 1) != (header.Previous == nil) ||
		(header.Previous != nil &&
			validateSegmentReference(*header.Previous) != nil) ||
		(header.Stream == SegmentStreamMessages) != (cursorBytes > 0) {
		return segmentBatchHeader{}, 0, 0, fmt.Errorf(
			"%w: segment batch header is inconsistent", ErrInvalidObject,
		)
	}
	return header, cursorBytes, recordBytes, nil
}

func validateSegmentBatch(batch SegmentBatch) error {
	if batch.Stream != SegmentStreamMetadata && batch.Stream != SegmentStreamMessages {
		return fmt.Errorf("%w: segment batch stream is invalid", ErrInvalidObject)
	}
	if err := validateRestorePointID(batch.Generation); err != nil {
		return fmt.Errorf("%w: segment batch generation: %v", ErrInvalidObject, err)
	}
	if batch.Sequence == 0 || batch.SourceHighWatermark == 0 || batch.WatermarkAtUnixMillis <= 0 || len(batch.Records) == 0 {
		return fmt.Errorf("%w: segment batch boundary is incomplete", ErrInvalidObject)
	}
	if err := validateSegmentBatchCursor(batch.FromCursor); err != nil {
		return err
	}
	if err := validateSegmentBatchCursor(batch.NextCursor); err != nil || batch.NextCursor == batch.FromCursor {
		return fmt.Errorf("%w: segment batch next cursor is invalid", ErrInvalidObject)
	}
	if batch.Sequence == 1 {
		if batch.Previous != nil {
			return fmt.Errorf("%w: first segment batch has a previous reference", ErrInvalidObject)
		}
	} else if batch.Previous == nil {
		return fmt.Errorf("%w: segment batch previous reference is required", ErrInvalidObject)
	}
	if batch.Previous != nil {
		if err := validateSegmentReference(*batch.Previous); err != nil {
			return err
		}
	}
	for index, record := range batch.Records {
		if len(record) == 0 || len(record) > maxObjectPlaintextBytes {
			return fmt.Errorf("%w: segment batch record[%d] size is invalid", ErrInvalidObject, index)
		}
	}
	if batch.Stream == SegmentStreamMessages {
		if len(batch.MessageCursors) == 0 {
			return fmt.Errorf("%w: message segment cursor index is required", ErrInvalidObject)
		}
	} else if len(batch.MessageCursors) != 0 {
		return fmt.Errorf("%w: metadata segment cannot contain message cursors", ErrInvalidObject)
	}
	return nil
}

func validateSegmentBatchCursor(cursor string) error {
	if len(cursor) > maxSegmentBatchCursorBytes || !utf8.ValidString(cursor) {
		return fmt.Errorf("%w: segment batch cursor is invalid", ErrInvalidObject)
	}
	return nil
}

func marshalSegmentBatchRecords(records [][]byte) ([]byte, error) {
	total := 0
	for _, record := range records {
		if len(record) > math.MaxUint32 || total > maxObjectPlaintextBytes-4-len(record) {
			return nil, fmt.Errorf("%w: segment batch records exceed size limit", ErrInvalidObject)
		}
		total += 4 + len(record)
	}
	body := make([]byte, 0, total)
	for _, record := range records {
		body = binary.BigEndian.AppendUint32(body, uint32(len(record)))
		body = append(body, record...)
	}
	return body, nil
}

func loadSegmentBatchRecords(body []byte, count uint64) ([][]byte, error) {
	if count == 0 || count > uint64(len(body)/5+1) || count > math.MaxInt {
		return nil, fmt.Errorf("%w: segment batch record count is invalid", ErrInvalidObject)
	}
	reader := bytes.NewReader(body)
	records := make([][]byte, 0, int(count))
	for index := uint64(0); index < count; index++ {
		var size uint32
		if err := binary.Read(reader, binary.BigEndian, &size); err != nil || size == 0 || uint64(size) > uint64(reader.Len()) {
			return nil, fmt.Errorf("%w: segment batch record %d is truncated", ErrInvalidObject, index)
		}
		record := make([]byte, size)
		if _, err := io.ReadFull(reader, record); err != nil {
			return nil, fmt.Errorf("%w: segment batch record %d is truncated", ErrInvalidObject, index)
		}
		records = append(records, record)
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("%w: trailing segment batch record data", ErrInvalidObject)
	}
	return records, nil
}

func cloneSegmentReference(reference *SegmentReference) *SegmentReference {
	if reference == nil {
		return nil
	}
	copy := *reference
	return &copy
}
