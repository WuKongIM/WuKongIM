package raftstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

// recoverLegacyPrefix admits only the old writer's pruned-prefix defect. It
// verifies every retained body record and a durable snapshot/commit proof before
// opening a file for append. No source record or checksum is rewritten.
func recoverLegacyPrefix(ctx context.Context, cfg Config, wc walConfig, meta metadata, original error) (*wal, error) {
	fail := func(reason string) (*wal, error) {
		return nil, fmt.Errorf("%w: legacy prefix recovery refused: %s", original, reason)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(wc.Dir, legacyAnchorFile)); err == nil {
		return fail("an existing validated anchor cannot be replaced after a CRC failure")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if meta.Version != metadataVersion || meta.NodeID != cfg.NodeID || meta.Snapshot.Index == 0 || meta.Snapshot.Term == 0 || meta.HardState.Term < meta.Snapshot.Term || meta.AppliedIndex < meta.Snapshot.Index || meta.HardState.Commit < meta.AppliedIndex {
		return fail("invalid durable boundaries or node identity")
	}
	files, err := walSegmentFiles(wc.Dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return fail("missing WAL")
	}
	seq, first, err := parseSegmentName(filepath.Base(files[0]))
	if err != nil {
		return nil, err
	}
	if seq == 0 || first > meta.Snapshot.Index+1 {
		return fail("snapshot does not cover the missing prefix")
	}
	expectedPath := filepath.Join(cfg.Dir, "snap", snapshotFileName(meta.Snapshot.Index, meta.Snapshot.Term))
	if meta.Snapshot.Path != expectedPath {
		return fail("snapshot path does not match this store")
	}
	data, err := os.ReadFile(expectedPath)
	if err != nil {
		return fail("snapshot unavailable")
	}
	var envelope snapshotEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fail("invalid snapshot envelope")
	}
	sum := sha256.Sum256(envelope.Data)
	if envelope.Version != snapshotVersion || len(envelope.Data) == 0 || envelope.Checksum != hex.EncodeToString(sum[:]) || envelope.Metadata.Index != meta.Snapshot.Index || envelope.Metadata.Term != meta.Snapshot.Term {
		return fail("snapshot checksum or identity mismatch")
	}
	wc.legacyPrefix = true
	w := &wal{cfg: wc}
	// The probe is strict and read-only, including its newest physical tail.
	replayed, err := w.replayMode(false)
	if err != nil {
		return fail("retained WAL did not validate: " + err.Error())
	}
	if !w.legacyHeaderAccepted {
		return fail("not a legacy first-header CRC mismatch")
	}
	if replayed.HardState != meta.HardState || replayed.AppliedIndex != meta.AppliedIndex || !reflect.DeepEqual(replayed.Snapshot, envelope.Metadata) || !reflect.DeepEqual(replayed.ConfState, meta.ConfState) {
		return fail("WAL and durable metadata disagree")
	}
	next, term := meta.Snapshot.Index+1, meta.Snapshot.Term
	for _, entry := range replayed.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Index <= meta.Snapshot.Index {
			continue
		}
		if entry.Index != next || entry.Term < term || entry.Term > meta.HardState.Term {
			return fail("missing or inconsistent snapshot suffix")
		}
		next++
		term = entry.Term
	}
	if next <= meta.HardState.Commit {
		return fail("committed suffix is incomplete")
	}
	// Commit compatibility proof before the first post-upgrade write. The WAL
	// may legitimately advance beyond meta.json if a later operation crashes.
	f, err := os.Open(files[0])
	if err != nil {
		return nil, err
	}
	_, headerCRC, err := readRecordMode(f, 0, true)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	if err := w.rememberLegacyAnchor(files[0], headerCRC); err != nil {
		return nil, err
	}
	w.cfg.legacyPrefix = false
	if err := w.loadTailState(files); err != nil {
		return nil, err
	}
	return w.openTail(files[len(files)-1])
}
