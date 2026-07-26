package backup

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"math"
)

const (
	// RestoreEvidenceVersion identifies the single-pass Slot restore evidence
	// computed while authenticated plaintext is installed.
	RestoreEvidenceVersion uint32 = 1

	restoreEvidenceMetadataDomain byte = 1
	restoreEvidenceMessageDomain  byte = 2
	restoreEvidenceSnapshotDomain byte = 3
	restoreMerkleLeafDomain       byte = 0
	restoreMerkleNodeDomain       byte = 1
)

// RestoreEvidence is the bounded semantic result of one Slot import.
type RestoreEvidence struct {
	// Version distinguishes explicit empty evidence from missing evidence.
	Version uint32
	// PlainBytes is the exact portable record payload consumed by the importer.
	PlainBytes uint64
	// MetadataRecords and MessageRecords are exact typed record counts.
	MetadataRecords uint64
	MessageRecords  uint64
	// MessageBoundaryRecords counts cursor-only message records.
	MessageBoundaryRecords uint64
	// ChannelBoundaryCount is the number of distinct restored Channels.
	ChannelBoundaryCount uint64
	// MaxMessageID is the greatest imported durable message identity.
	MaxMessageID uint64
	// ContentSHA256 authenticates the ordered typed record stream.
	ContentSHA256 string
	// MessageMerkleSHA256 authenticates imported message leaves in replay order.
	MessageMerkleSHA256 string
}

// AddMetadataSnapshot validates and accounts for one materialized metadata
// key/value row before incremental committed commands are replayed.
func (a *RestoreEvidenceAccumulator) AddMetadataSnapshot(
	key []byte,
	value []byte,
) error {
	if a == nil || a.finished || len(key) == 0 {
		return fmt.Errorf(
			"%w: restore evidence accumulator is closed or snapshot row is invalid",
			ErrInvalidObject,
		)
	}
	if len(key) > math.MaxInt-len(value) {
		return fmt.Errorf("%w: restore snapshot row size overflow", ErrInvalidObject)
	}
	if err := a.addPlainBytes(len(key) + len(value)); err != nil {
		return err
	}
	writeRestoreEvidencePair(
		a.content, restoreEvidenceSnapshotDomain, key, value,
	)
	a.evidence.MetadataRecords++
	return nil
}

// RestoreEvidenceAccumulator validates portable records and computes restore
// evidence without retaining message payloads.
type RestoreEvidenceAccumulator struct {
	hashSlot  uint16
	content   hash.Hash
	evidence  RestoreEvidence
	index     RestoreEvidenceIndex
	ownsIndex bool
	raftIndex uint64
	raftTerm  uint64
	merkle    restoreMerkleAccumulator
	finished  bool
}

type restoreChannelIdentity struct {
	channelType uint8
	channelID   string
}

// RestoreEvidenceIndex validates per-Channel cursor and message monotonicity
// without prescribing an in-memory implementation. Production restore uses a
// disk-backed implementation so Channel cardinality cannot escape its memory
// budget.
type RestoreEvidenceIndex interface {
	ObserveCursor(ChannelBoundary) error
	ObserveMessage(ChannelBoundary, uint64) error
	ChannelCount() uint64
	Close() error
}

// NewRestoreEvidenceAccumulator creates one Hash-Slot-scoped accumulator.
func NewRestoreEvidenceAccumulator(hashSlot uint16) *RestoreEvidenceAccumulator {
	return NewRestoreEvidenceAccumulatorWithIndex(hashSlot, nil)
}

// NewRestoreEvidenceAccumulatorWithIndex creates an accumulator using index.
// A nil index selects the test-oriented in-memory implementation.
func NewRestoreEvidenceAccumulatorWithIndex(
	hashSlot uint16,
	index RestoreEvidenceIndex,
) *RestoreEvidenceAccumulator {
	ownsIndex := false
	if index == nil {
		index = newMemoryRestoreEvidenceIndex()
		ownsIndex = true
	}
	return &RestoreEvidenceAccumulator{
		hashSlot: hashSlot, content: sha256.New(),
		evidence: RestoreEvidence{Version: RestoreEvidenceVersion},
		index:    index, ownsIndex: ownsIndex,
	}
}

// AddMetadata validates and accounts for one committed metadata record.
func (a *RestoreEvidenceAccumulator) AddMetadata(body []byte) (MetadataLogRecord, error) {
	if a == nil || a.finished {
		return MetadataLogRecord{}, fmt.Errorf("%w: restore evidence accumulator is closed", ErrInvalidObject)
	}
	record, err := LoadMetadataLogRecord(body)
	if err != nil {
		return MetadataLogRecord{}, err
	}
	if record.HashSlot != a.hashSlot {
		return MetadataLogRecord{}, fmt.Errorf("%w: restore metadata hash slot mismatch", ErrInvalidObject)
	}
	if record.RaftIndex <= a.raftIndex ||
		(a.raftTerm != 0 && record.RaftTerm < a.raftTerm) {
		return MetadataLogRecord{}, fmt.Errorf(
			"%w: restore metadata order regressed",
			ErrObjectCorrupt,
		)
	}
	if err := a.addPlainBytes(len(body)); err != nil {
		return MetadataLogRecord{}, err
	}
	writeRestoreEvidenceFrame(a.content, restoreEvidenceMetadataDomain, body)
	a.raftIndex = record.RaftIndex
	a.raftTerm = record.RaftTerm
	a.evidence.MetadataRecords++
	return record, nil
}

// AddMessage validates and accounts for one committed message or boundary record.
func (a *RestoreEvidenceAccumulator) AddMessage(body []byte) (MessageLogRecord, error) {
	if a == nil || a.finished {
		return MessageLogRecord{}, fmt.Errorf("%w: restore evidence accumulator is closed", ErrInvalidObject)
	}
	record, err := LoadMessageLogRecord(body)
	if err != nil {
		return MessageLogRecord{}, err
	}
	if record.HashSlot != a.hashSlot {
		return MessageLogRecord{}, fmt.Errorf("%w: restore message hash slot mismatch", ErrInvalidObject)
	}
	if err := a.addPlainBytes(len(body)); err != nil {
		return MessageLogRecord{}, err
	}
	boundary := ChannelBoundary{
		ChannelID: record.ChannelID, ChannelType: record.ChannelType,
		Epoch: record.Epoch, LogStartOffset: record.LogStartOffset, HW: record.HW,
	}
	messageSequence := uint64(0)
	if record.Kind == MessageLogRecordMessage {
		messageSequence = record.MessageSeq
	}
	if err := a.index.ObserveMessage(boundary, messageSequence); err != nil {
		return MessageLogRecord{}, err
	}
	writeRestoreEvidenceFrame(a.content, restoreEvidenceMessageDomain, body)
	switch record.Kind {
	case MessageLogRecordMessage:
		a.evidence.MessageRecords++
		if record.MessageID > a.evidence.MaxMessageID {
			a.evidence.MaxMessageID = record.MessageID
		}
		a.merkle.add(body)
	case MessageLogRecordBoundary:
		a.evidence.MessageBoundaryRecords++
	default:
		return MessageLogRecord{}, fmt.Errorf("%w: restore message kind is invalid", ErrInvalidObject)
	}
	return record, nil
}

// MergeBoundary merges authenticated cursor evidence without counting it as a
// second logical record.
func (a *RestoreEvidenceAccumulator) MergeBoundary(boundary ChannelBoundary) error {
	return a.MergeCursorBoundary(boundary)
}

// MergeCursorBoundary merges an authenticated cursor index entry. Cursor
// indexes are physically encoded before their segment records, so this method
// validates cursor order independently while still requiring the cursor to
// cover every subsequently replayed record.
func (a *RestoreEvidenceAccumulator) MergeCursorBoundary(
	boundary ChannelBoundary,
) error {
	if a == nil || a.finished || boundary.ChannelID == "" || boundary.ChannelType == 0 ||
		boundary.Epoch == 0 || boundary.LogStartOffset > boundary.HW {
		return fmt.Errorf("%w: restore Channel boundary is invalid", ErrInvalidObject)
	}
	return a.index.ObserveCursor(boundary)
}

func restoreBoundaryAdvances(
	previous ChannelBoundary,
	next ChannelBoundary,
) bool {
	return next.Epoch >= previous.Epoch &&
		next.LogStartOffset >= previous.LogStartOffset &&
		next.HW >= previous.HW
}

func restoreBoundaryCovers(
	cover ChannelBoundary,
	covered ChannelBoundary,
) bool {
	return cover.Epoch >= covered.Epoch &&
		cover.LogStartOffset >= covered.LogStartOffset &&
		cover.HW >= covered.HW
}

type memoryRestoreEvidenceIndex struct {
	boundaries        map[restoreChannelIdentity]ChannelBoundary
	messageBoundaries map[restoreChannelIdentity]ChannelBoundary
	cursorBoundaries  map[restoreChannelIdentity]ChannelBoundary
	messageSeq        map[restoreChannelIdentity]uint64
}

func newMemoryRestoreEvidenceIndex() *memoryRestoreEvidenceIndex {
	return &memoryRestoreEvidenceIndex{
		boundaries:        make(map[restoreChannelIdentity]ChannelBoundary),
		messageBoundaries: make(map[restoreChannelIdentity]ChannelBoundary),
		cursorBoundaries:  make(map[restoreChannelIdentity]ChannelBoundary),
		messageSeq:        make(map[restoreChannelIdentity]uint64),
	}
}

func (i *memoryRestoreEvidenceIndex) ObserveCursor(
	boundary ChannelBoundary,
) error {
	identity := restoreChannelIdentity{
		channelType: boundary.ChannelType,
		channelID:   boundary.ChannelID,
	}
	if previous, found := i.cursorBoundaries[identity]; found &&
		!restoreBoundaryAdvances(previous, boundary) {
		return fmt.Errorf("%w: restore Channel cursor regressed", ErrObjectCorrupt)
	}
	if messages, found := i.messageBoundaries[identity]; found &&
		!restoreBoundaryCovers(boundary, messages) {
		return fmt.Errorf(
			"%w: restore Channel cursor does not cover messages",
			ErrObjectCorrupt,
		)
	}
	i.cursorBoundaries[identity] = boundary
	i.boundaries[identity] = boundary
	return nil
}

func (i *memoryRestoreEvidenceIndex) ObserveMessage(
	boundary ChannelBoundary,
	messageSequence uint64,
) error {
	identity := restoreChannelIdentity{
		channelType: boundary.ChannelType,
		channelID:   boundary.ChannelID,
	}
	if messageSequence > 0 && messageSequence <= i.messageSeq[identity] {
		return fmt.Errorf(
			"%w: restore message sequence regressed or duplicated",
			ErrObjectCorrupt,
		)
	}
	if previous, found := i.messageBoundaries[identity]; found &&
		!restoreBoundaryAdvances(previous, boundary) {
		return fmt.Errorf(
			"%w: restore Channel message boundary regressed",
			ErrObjectCorrupt,
		)
	}
	if cursor, found := i.cursorBoundaries[identity]; found {
		if !restoreBoundaryCovers(cursor, boundary) {
			return fmt.Errorf(
				"%w: restore Channel message exceeds cursor",
				ErrObjectCorrupt,
			)
		}
	} else {
		i.boundaries[identity] = boundary
	}
	if messageSequence > 0 {
		i.messageSeq[identity] = messageSequence
	}
	i.messageBoundaries[identity] = boundary
	return nil
}

func (i *memoryRestoreEvidenceIndex) ChannelCount() uint64 {
	return uint64(len(i.boundaries))
}

func (*memoryRestoreEvidenceIndex) Close() error { return nil }

// Finish returns detached evidence after closing any accumulator-owned index.
func (a *RestoreEvidenceAccumulator) Finish() (RestoreEvidence, error) {
	if a == nil || a.finished {
		return RestoreEvidence{}, fmt.Errorf("%w: restore evidence accumulator is closed", ErrInvalidObject)
	}
	a.finished = true
	a.evidence.ChannelBoundaryCount = a.index.ChannelCount()
	a.evidence.ContentSHA256 = hex.EncodeToString(a.content.Sum(nil))
	merkleRoot := a.merkle.root()
	a.evidence.MessageMerkleSHA256 = hex.EncodeToString(merkleRoot[:])
	if (a.evidence.MessageRecords == 0) != (a.evidence.MaxMessageID == 0) {
		return RestoreEvidence{}, fmt.Errorf("%w: restore message identity evidence is inconsistent", ErrObjectCorrupt)
	}
	if a.ownsIndex {
		if err := a.index.Close(); err != nil {
			return RestoreEvidence{}, err
		}
	}
	return a.evidence, nil
}

func (a *RestoreEvidenceAccumulator) addPlainBytes(size int) error {
	if size <= 0 || uint64(size) > math.MaxUint64-a.evidence.PlainBytes {
		return fmt.Errorf("%w: restore plaintext size overflow", ErrInvalidObject)
	}
	a.evidence.PlainBytes += uint64(size)
	return nil
}

func writeRestoreEvidenceFrame(target hash.Hash, domain byte, body []byte) {
	_, _ = target.Write([]byte{domain})
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(body)))
	_, _ = target.Write(size[:])
	_, _ = target.Write(body)
}

func writeRestoreEvidencePair(
	target hash.Hash,
	domain byte,
	left []byte,
	right []byte,
) {
	var header [1 + 8]byte
	header[0] = domain
	binary.BigEndian.PutUint64(header[1:], uint64(len(left)))
	_, _ = target.Write(header[:])
	_, _ = target.Write(left)
	binary.BigEndian.PutUint64(header[1:], uint64(len(right)))
	_, _ = target.Write(header[:])
	_, _ = target.Write(right)
}

type restoreMerkleAccumulator struct {
	levels [64]*[sha256.Size]byte
	count  uint64
}

func (a *restoreMerkleAccumulator) add(body []byte) {
	leaf := sha256.New()
	_, _ = leaf.Write([]byte{restoreMerkleLeafDomain})
	_, _ = leaf.Write(body)
	var node [sha256.Size]byte
	copy(node[:], leaf.Sum(nil))
	level := 0
	for a.levels[level] != nil {
		node = restoreMerkleNode(*a.levels[level], node)
		a.levels[level] = nil
		level++
	}
	copyNode := node
	a.levels[level] = &copyNode
	a.count++
}

func (a *restoreMerkleAccumulator) root() [sha256.Size]byte {
	if a.count == 0 {
		return sha256.Sum256(nil)
	}
	var root *[sha256.Size]byte
	for level := 0; level < len(a.levels); level++ {
		if a.levels[level] == nil {
			continue
		}
		if root == nil {
			copyNode := *a.levels[level]
			root = &copyNode
			continue
		}
		combined := restoreMerkleNode(*a.levels[level], *root)
		root = &combined
	}
	return *root
}

func restoreMerkleNode(left, right [sha256.Size]byte) [sha256.Size]byte {
	var body [1 + 2*sha256.Size]byte
	body[0] = restoreMerkleNodeDomain
	copy(body[1:1+sha256.Size], left[:])
	copy(body[1+sha256.Size:], right[:])
	return sha256.Sum256(body[:])
}
