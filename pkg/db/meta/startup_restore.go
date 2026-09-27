package meta

import (
	"context"
	"encoding/binary"
	"errors"
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// ErrRestoreIncomplete prevents trusting the applied watermark of a partially
// installed startup snapshot. Reinstalling a verified snapshot clears it.
var ErrRestoreIncomplete = errors.New("metadata startup snapshot installation incomplete")

// SnapshotRestoreProgress contains no keys, credentials, or record payloads.
type SnapshotRestoreProgress struct {
	Stage                 string
	Bytes, TotalBytes     int64
	Entries, TotalEntries int64
}

type startupRestore struct {
	slotID, index uint64
	report        func(SnapshotRestoreProgress)
}

// RestoreStartupSnapshot installs an immutable snapshot in bounded batches.
// The caller must keep this physical Slot unregistered and reject all reads
// and writes until it returns successfully. Partial installation is durable
// but never usable: the pending marker forces a full retry on the next open.
func (db *MetaDB) RestoreStartupSnapshot(ctx context.Context, slotID, index uint64, hashSlots []uint16, reader io.ReadSeeker, size int64, report func(SnapshotRestoreProgress)) error {
	if slotID == 0 || index == 0 {
		return ErrInvalidArgument
	}
	return db.importHashSlotSnapshotReader(ctx, hashSlots, reader, size, portableSnapshotAllEntries, false, false, nil, &startupRestore{slotID: slotID, index: index, report: report})
}

func encodeSlotRestorePendingKey(slotID uint64) []byte {
	var builder keycodec.Builder
	key := builder.Reset().Domain(keycodec.DomainMeta).Partition(keycodec.PartitionGlobal, nil).System(0, systemIDSlotRestorePending).Key()
	return keycodec.AppendUint64(key, slotID)
}
func slotIndexValue(index uint64) []byte {
	var value [8]byte
	binary.BigEndian.PutUint64(value[:], index)
	return value[:]
}

// snapshotValidationReader reports actual stream offsets without retaining
// records. It is disabled once installation begins so batch progress represents
// durable writes rather than bytes merely read ahead.
type snapshotValidationReader struct {
	io.ReadSeeker
	ctx       context.Context
	size, pos int64
	stage     string
	report    func(SnapshotRestoreProgress)
}

func (r *snapshotValidationReader) Read(p []byte) (int, error) {
	if err := contextErr(r.ctx); err != nil {
		return 0, err
	}
	n, err := r.ReadSeeker.Read(p)
	r.pos += int64(n)
	if r.stage != "" {
		r.report(SnapshotRestoreProgress{Stage: r.stage, Bytes: r.pos, TotalBytes: r.size})
	}
	return n, err
}
func (r *snapshotValidationReader) Seek(offset int64, whence int) (int64, error) {
	pos, err := r.ReadSeeker.Seek(offset, whence)
	if err == nil {
		r.pos = pos
	}
	return pos, err
}
