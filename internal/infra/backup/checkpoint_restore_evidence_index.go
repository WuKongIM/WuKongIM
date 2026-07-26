package backup

import (
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/cockroachdb/pebble/v2"
)

const (
	restoreEvidenceIndexValueBytes   = 1 + 8*8
	restoreEvidenceCursorPresent     = 1 << 0
	restoreEvidenceMessagePresent    = 1 << 1
	restoreEvidenceTargetInitialized = 1 << 2
)

// checkpointRestoreEvidenceIndex keeps per-Channel restore ordering on disk.
// Its fixed-size values avoid retaining a million-Channel boundary map on the
// Go heap while authenticated records stream through one Slot.
type checkpointRestoreEvidenceIndex struct {
	// mu serializes Pebble access and close.
	mu sync.Mutex
	// db stores fixed-size Channel boundary state.
	db *pebble.DB
	// count tracks distinct Channels while a writable index is built.
	count uint64
	// readOnly prevents verification opens from mutating retained staging.
	readOnly bool
}

type checkpointRestoreChannelState struct {
	cursorPresent     bool
	messagePresent    bool
	cursor            backupartifact.ChannelBoundary
	message           backupartifact.ChannelBoundary
	messageSeq        uint64
	erasureThrough    uint64
	targetInitialized bool
}

func (i *checkpointRestoreEvidenceIndex) claimTargetInitialization(
	boundary backupartifact.ChannelBoundary,
) (bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	state, found, err := i.load(boundary)
	if err != nil {
		return false, err
	}
	if state.targetInitialized {
		return false, nil
	}
	state.targetInitialized = true
	if err := i.store(boundary, state, found); err != nil {
		return false, err
	}
	return true, nil
}

func (i *checkpointRestoreEvidenceIndex) applyErasure(
	boundary backupartifact.ChannelBoundary,
) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	state, found, err := i.load(boundary)
	if err != nil {
		return err
	}
	if state.erasureThrough >= boundary.LogStartOffset {
		return nil
	}
	current := state.cursor
	if !state.cursorPresent {
		current = state.message
	}
	if boundary.Epoch == 0 {
		boundary.Epoch = 1
		if state.cursorPresent || state.messagePresent {
			boundary.Epoch = current.Epoch
		}
	}
	if (state.cursorPresent || state.messagePresent) &&
		boundary.Epoch < current.Epoch {
		return fmt.Errorf(
			"%w: restore erasure epoch regressed",
			backupartifact.ErrObjectCorrupt,
		)
	}
	if boundary.LogStartOffset > boundary.HW {
		boundary.HW = boundary.LogStartOffset
	}
	if current.Epoch > boundary.Epoch {
		boundary.Epoch = current.Epoch
	}
	if current.LogStartOffset > boundary.LogStartOffset {
		boundary.LogStartOffset = current.LogStartOffset
	}
	if current.HW > boundary.HW {
		boundary.HW = current.HW
	}
	if boundary.LogStartOffset > state.erasureThrough {
		state.erasureThrough = boundary.LogStartOffset
	}
	state.cursorPresent = true
	state.cursor = boundary
	return i.store(boundary, state, found)
}

func (i *checkpointRestoreEvidenceIndex) boundary(
	channelID string,
	channelType uint8,
) (backupartifact.ChannelBoundary, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	identity := backupartifact.ChannelBoundary{
		ChannelID: channelID, ChannelType: channelType,
	}
	state, found, err := i.load(identity)
	if err != nil {
		return backupartifact.ChannelBoundary{}, err
	}
	if !found {
		return backupartifact.ChannelBoundary{}, fmt.Errorf(
			"%w: restore boundary is missing",
			backupartifact.ErrObjectCorrupt,
		)
	}
	if state.cursorPresent {
		return state.cursor, nil
	}
	return state.message, nil
}

func openCheckpointRestoreEvidenceIndex(
	path string,
) (*checkpointRestoreEvidenceIndex, error) {
	return openCheckpointRestoreEvidenceIndexMode(path, false)
}

// openCheckpointRestoreEvidenceIndexReadOnly opens completed boundary evidence
// without creating MANIFEST, OPTIONS, or WAL files during status verification.
func openCheckpointRestoreEvidenceIndexReadOnly(
	path string,
) (*checkpointRestoreEvidenceIndex, error) {
	return openCheckpointRestoreEvidenceIndexMode(path, true)
}

func openCheckpointRestoreEvidenceIndexMode(
	path string,
	readOnly bool,
) (*checkpointRestoreEvidenceIndex, error) {
	if path == "" {
		return nil, fmt.Errorf("backup restore evidence index: path is required")
	}
	db, err := pebble.Open(filepath.Clean(path), &pebble.Options{
		CacheSize:                   1 << 20,
		MemTableSize:                1 << 20,
		MemTableStopWritesThreshold: 2,
		DisableWAL:                  true,
		ReadOnly:                    readOnly,
	})
	if err != nil {
		return nil, err
	}
	return &checkpointRestoreEvidenceIndex{
		db: db, readOnly: readOnly,
	}, nil
}

func (i *checkpointRestoreEvidenceIndex) ObserveCursor(
	boundary backupartifact.ChannelBoundary,
) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	state, found, err := i.load(boundary)
	if err != nil {
		return err
	}
	if state.cursorPresent &&
		!checkpointRestoreBoundaryAdvances(state.cursor, boundary) {
		return fmt.Errorf(
			"%w: restore Channel cursor regressed",
			backupartifact.ErrObjectCorrupt,
		)
	}
	if state.messagePresent &&
		!checkpointRestoreBoundaryCovers(boundary, state.message) {
		return fmt.Errorf(
			"%w: restore Channel cursor does not cover messages",
			backupartifact.ErrObjectCorrupt,
		)
	}
	state.cursorPresent = true
	state.cursor = boundary
	return i.store(boundary, state, found)
}

func (i *checkpointRestoreEvidenceIndex) ObserveMessage(
	boundary backupartifact.ChannelBoundary,
	messageSequence uint64,
) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	state, found, err := i.load(boundary)
	if err != nil {
		return err
	}
	if messageSequence > 0 && messageSequence <= state.messageSeq {
		return fmt.Errorf(
			"%w: restore message sequence regressed or duplicated",
			backupartifact.ErrObjectCorrupt,
		)
	}
	if state.messagePresent &&
		!checkpointRestoreBoundaryAdvances(state.message, boundary) {
		return fmt.Errorf(
			"%w: restore Channel message boundary regressed",
			backupartifact.ErrObjectCorrupt,
		)
	}
	if state.cursorPresent &&
		!checkpointRestoreBoundaryCovers(state.cursor, boundary) {
		return fmt.Errorf(
			"%w: restore Channel message exceeds cursor",
			backupartifact.ErrObjectCorrupt,
		)
	}
	state.messagePresent = true
	state.message = boundary
	if messageSequence > 0 {
		state.messageSeq = messageSequence
	}
	return i.store(boundary, state, found)
}

func (i *checkpointRestoreEvidenceIndex) ChannelCount() uint64 {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.count
}

func (i *checkpointRestoreEvidenceIndex) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.db == nil {
		return nil
	}
	db := i.db
	i.db = nil
	if i.readOnly {
		return db.Close()
	}
	return errors.Join(db.Flush(), db.Close())
}

func (i *checkpointRestoreEvidenceIndex) VisitBoundaries(
	visit func(backupartifact.ChannelBoundary) error,
) error {
	return i.visitBoundaries(
		func(boundary backupartifact.ChannelBoundary, _ uint64) error {
			return visit(boundary)
		},
	)
}

func (i *checkpointRestoreEvidenceIndex) visitBoundaries(
	visit func(backupartifact.ChannelBoundary, uint64) error,
) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.db == nil || visit == nil {
		return fmt.Errorf("backup restore evidence index: unavailable")
	}
	iter, err := i.db.NewIter(nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for valid := iter.First(); valid; valid = iter.Next() {
		key := iter.Key()
		if len(key) < 2 {
			return fmt.Errorf(
				"%w: restore evidence index key is corrupt",
				backupartifact.ErrObjectCorrupt,
			)
		}
		state, err := decodeCheckpointRestoreChannelState(
			key[0], string(key[1:]), iter.Value(),
		)
		if err != nil {
			return err
		}
		boundary := state.message
		if state.cursorPresent {
			boundary = state.cursor
		}
		if err := visit(boundary, state.erasureThrough); err != nil {
			return err
		}
	}
	return iter.Error()
}

func (i *checkpointRestoreEvidenceIndex) load(
	boundary backupartifact.ChannelBoundary,
) (checkpointRestoreChannelState, bool, error) {
	if i.db == nil {
		return checkpointRestoreChannelState{}, false,
			fmt.Errorf("backup restore evidence index: closed")
	}
	key := checkpointRestoreEvidenceKey(boundary)
	value, closer, err := i.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return checkpointRestoreChannelState{}, false, nil
	}
	if err != nil {
		return checkpointRestoreChannelState{}, false, err
	}
	defer closer.Close()
	state, err := decodeCheckpointRestoreChannelState(
		boundary.ChannelType, boundary.ChannelID, value,
	)
	return state, true, err
}

func (i *checkpointRestoreEvidenceIndex) store(
	boundary backupartifact.ChannelBoundary,
	state checkpointRestoreChannelState,
	found bool,
) error {
	value := encodeCheckpointRestoreChannelState(state)
	if err := i.db.Set(
		checkpointRestoreEvidenceKey(boundary), value, pebble.NoSync,
	); err != nil {
		return err
	}
	if !found {
		i.count++
	}
	return nil
}

func checkpointRestoreEvidenceKey(
	boundary backupartifact.ChannelBoundary,
) []byte {
	key := make([]byte, 1+len(boundary.ChannelID))
	key[0] = boundary.ChannelType
	copy(key[1:], boundary.ChannelID)
	return key
}

func encodeCheckpointRestoreChannelState(
	state checkpointRestoreChannelState,
) []byte {
	body := make([]byte, restoreEvidenceIndexValueBytes)
	if state.cursorPresent {
		body[0] |= restoreEvidenceCursorPresent
	}
	if state.messagePresent {
		body[0] |= restoreEvidenceMessagePresent
	}
	if state.targetInitialized {
		body[0] |= restoreEvidenceTargetInitialized
	}
	values := [...]uint64{
		state.cursor.Epoch,
		state.cursor.LogStartOffset,
		state.cursor.HW,
		state.message.Epoch,
		state.message.LogStartOffset,
		state.message.HW,
		state.messageSeq,
		state.erasureThrough,
	}
	for index, value := range values {
		binary.BigEndian.PutUint64(body[1+index*8:], value)
	}
	return body
}

func decodeCheckpointRestoreChannelState(
	channelType uint8,
	channelID string,
	body []byte,
) (checkpointRestoreChannelState, error) {
	if channelType == 0 || channelID == "" ||
		len(body) != restoreEvidenceIndexValueBytes ||
		body[0]&^(restoreEvidenceCursorPresent|
			restoreEvidenceMessagePresent|
			restoreEvidenceTargetInitialized) != 0 {
		return checkpointRestoreChannelState{}, fmt.Errorf(
			"%w: restore evidence index value is corrupt",
			backupartifact.ErrObjectCorrupt,
		)
	}
	state := checkpointRestoreChannelState{
		cursorPresent:     body[0]&restoreEvidenceCursorPresent != 0,
		messagePresent:    body[0]&restoreEvidenceMessagePresent != 0,
		targetInitialized: body[0]&restoreEvidenceTargetInitialized != 0,
	}
	values := [8]uint64{}
	for index := range values {
		values[index] = binary.BigEndian.Uint64(body[1+index*8:])
	}
	state.cursor = backupartifact.ChannelBoundary{
		ChannelID: channelID, ChannelType: channelType,
		Epoch: values[0], LogStartOffset: values[1], HW: values[2],
	}
	state.message = backupartifact.ChannelBoundary{
		ChannelID: channelID, ChannelType: channelType,
		Epoch: values[3], LogStartOffset: values[4], HW: values[5],
	}
	state.messageSeq = values[6]
	state.erasureThrough = values[7]
	if (!state.cursorPresent && !state.messagePresent) ||
		(state.cursorPresent &&
			(state.cursor.Epoch == 0 ||
				state.cursor.LogStartOffset > state.cursor.HW)) ||
		(state.messagePresent &&
			(state.message.Epoch == 0 ||
				state.message.LogStartOffset > state.message.HW)) {
		return checkpointRestoreChannelState{}, fmt.Errorf(
			"%w: restore evidence index boundary is corrupt",
			backupartifact.ErrObjectCorrupt,
		)
	}
	return state, nil
}

func checkpointRestoreBoundaryAdvances(
	previous backupartifact.ChannelBoundary,
	next backupartifact.ChannelBoundary,
) bool {
	return next.Epoch >= previous.Epoch &&
		next.LogStartOffset >= previous.LogStartOffset &&
		next.HW >= previous.HW
}

func checkpointRestoreBoundaryCovers(
	cover backupartifact.ChannelBoundary,
	covered backupartifact.ChannelBoundary,
) bool {
	return cover.Epoch >= covered.Epoch &&
		cover.LogStartOffset >= covered.LogStartOffset &&
		cover.HW >= covered.HW
}

var _ backupartifact.RestoreEvidenceIndex = (*checkpointRestoreEvidenceIndex)(nil)
