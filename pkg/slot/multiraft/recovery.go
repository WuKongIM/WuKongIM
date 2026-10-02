package multiraft

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.etcd.io/raft/v3/raftpb"
)

// RecoveryProgress contains body-free startup counters. A stage's counters are
// independent of other stages; completion never implies foreground readiness.
type RecoveryProgress struct {
	// SnapshotIndex identifies the pinned Raft boundary when known.
	SnapshotIndex         uint64
	Stage                 string
	Reason                string
	Bytes, TotalBytes     int64
	Entries, TotalEntries int64
}

// StartupSnapshot owns a pinned, immutable seekable payload until Reader.Close.
// An empty snapshot has zero Metadata.Index and a nil Reader.
type StartupSnapshot struct {
	Metadata raftpb.SnapshotMetadata
	Size     int64
	Reader   io.ReadSeekCloser
	// Digest covers the authenticated complete payload, including its envelope.
	Digest [32]byte
}

// StartupSnapshotStorage avoids materializing durable snapshot bytes at open.
// Implementations must authenticate the entire payload before returning it.
type StartupSnapshotStorage interface {
	OpenStartupSnapshot(context.Context, func(RecoveryProgress)) (StartupSnapshot, error)
}

// StartupSnapshotRestorer installs a snapshot before the Slot is registered.
// It must durably fence interrupted installs and publish the watermark only
// after every batch is durable. Runtime snapshot replacement still uses Restore.
type StartupSnapshotRestorer interface {
	RestoreStartupSnapshot(context.Context, Snapshot, io.ReadSeeker, int64, func(RecoveryProgress)) error
}

// recoveryReporter emits phase boundaries immediately and progress at most once
// every five seconds. Replay updates may arrive on a separate apply worker.
type recoveryReporter struct {
	mu            sync.Mutex
	logger        wklog.Logger
	nodeID        NodeID
	slotID        SlotID
	now           func() time.Time
	started, last time.Time
	progress      RecoveryProgress
	terminal      bool
}

func newRecoveryReporter(logger wklog.Logger, nodeID NodeID, slotID SlotID) *recoveryReporter {
	return &recoveryReporter{logger: logger, nodeID: nodeID, slotID: slotID, now: time.Now}
}
func (r *recoveryReporter) report(p RecoveryProgress) {
	if r == nil || r.logger == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminal {
		return
	}
	now := r.now()
	if r.started.IsZero() {
		r.started = now
	}
	emit := r.progress.Stage != p.Stage || r.last.IsZero() || now.Sub(r.last) >= 5*time.Second
	if p.SnapshotIndex == 0 {
		p.SnapshotIndex = r.progress.SnapshotIndex
	}
	r.progress = p
	if !emit {
		return
	}
	r.last = now
	r.logger.Info("Slot recovery progress", r.fields(now)...)
	if p.Stage == "complete" {
		r.terminal = true
	}
}
func (r *recoveryReporter) fields(now time.Time) []wklog.Field {
	p := r.progress
	fields := []wklog.Field{wklog.Event("slot.recovery.progress"), wklog.NodeID(uint64(r.nodeID)), wklog.SlotID(uint64(r.slotID)), wklog.String("stage", p.Stage), wklog.Uint64("snapshotIndex", p.SnapshotIndex), wklog.Int64("bytes", p.Bytes), wklog.Int64("totalBytes", p.TotalBytes), wklog.Int64("entries", p.Entries), wklog.Int64("totalEntries", p.TotalEntries), wklog.Duration("elapsed", now.Sub(r.started))}
	if p.Reason != "" {
		fields = append(fields, wklog.String("reason", p.Reason))
	}
	if p.TotalEntries > 0 {
		fields = append(fields, wklog.Float64("percent", 100*float64(p.Entries)/float64(p.TotalEntries)))
	} else if p.TotalBytes > 0 {
		fields = append(fields, wklog.Float64("percent", 100*float64(p.Bytes)/float64(p.TotalBytes)))
	}
	return fields
}
func (r *recoveryReporter) fail(err error) {
	if r == nil || r.logger == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminal {
		return
	}
	r.terminal = true
	fields := r.fields(r.now())
	fields = append(fields, wklog.Error(err))
	r.logger.Error("Slot recovery failed", fields...)
}

// payloadSection translates a legacy or enveloped snapshot into the portable
// metadata stream without copying it. Its owner closes the underlying reader.
type payloadSection struct {
	source            io.ReadSeeker
	offset, size, pos int64
}

func (s *payloadSection) Read(p []byte) (int, error) {
	if s.pos >= s.size {
		return 0, io.EOF
	}
	if int64(len(p)) > s.size-s.pos {
		p = p[:s.size-s.pos]
	}
	n, err := s.source.Read(p)
	s.pos += int64(n)
	return n, err
}
func (s *payloadSection) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = s.pos + offset
	case io.SeekEnd:
		next = s.size + offset
	default:
		return 0, errors.New("invalid snapshot seek origin")
	}
	if next < 0 || next > s.size {
		return 0, errors.New("snapshot seek out of range")
	}
	if _, err := s.source.Seek(s.offset+next, io.SeekStart); err != nil {
		return 0, err
	}
	s.pos = next
	return next, nil
}

// openRecoverySnapshot selects streaming only when both durable storage and
// the FSM support startup-only installation. Other adapters retain compatibility.
func openRecoverySnapshot(ctx context.Context, opts SlotOptions, report func(RecoveryProgress)) (BootstrapState, raftpb.Snapshot, *loadedMemoryStorage, *StartupSnapshot, error) {
	adapter := newStorageAdapter(opts.Storage)
	storage, hasReader := opts.Storage.(StartupSnapshotStorage)
	_, hasRestorer := opts.StateMachine.(StartupSnapshotRestorer)
	if !opts.StartupRecovery || !hasReader || !hasRestorer {
		state, snap, memory, err := adapter.load(ctx)
		return state, snap, memory, nil, err
	}
	state, err := opts.Storage.InitialState(ctx)
	if err != nil {
		return state, raftpb.Snapshot{}, nil, nil, err
	}
	opened, err := storage.OpenStartupSnapshot(ctx, report)
	if err != nil {
		return state, raftpb.Snapshot{}, nil, nil, err
	}
	snap := raftpb.Snapshot{Metadata: opened.Metadata}
	report(RecoveryProgress{Stage: "log_load"})
	state, snap, memory, err := adapter.loadSnapshot(ctx, state, snap)
	if err != nil {
		if opened.Reader != nil {
			_ = opened.Reader.Close()
		}
		return state, snap, memory, nil, err
	}
	return state, snap, memory, &opened, nil
}

func startupSnapshotPayload(opened *StartupSnapshot) (io.ReadSeeker, int64, uint64, error) {
	prefix := make([]byte, slotSnapshotDataHeaderSize)
	n, err := io.ReadFull(opened.Reader, prefix)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, 0, 0, err
	}
	prefix = prefix[:n]
	var offset int64
	var configIndex uint64
	if bytes.HasPrefix(prefix, []byte(slotSnapshotDataMagic)) {
		if len(prefix) != slotSnapshotDataHeaderSize {
			return nil, 0, 0, errors.New("slot snapshot envelope is truncated")
		}
		if prefix[len(slotSnapshotDataMagic)] != slotSnapshotDataVersion {
			return nil, 0, 0, fmt.Errorf("unsupported slot snapshot envelope version")
		}
		configIndex = binary.BigEndian.Uint64(prefix[len(slotSnapshotDataMagic)+1:])
		offset = int64(slotSnapshotDataHeaderSize)
	}
	section := &payloadSection{source: opened.Reader, offset: offset, size: opened.Size - offset}
	if _, err := section.Seek(0, io.SeekStart); err != nil {
		return nil, 0, 0, err
	}
	return section, section.size, configIndex, nil
}
