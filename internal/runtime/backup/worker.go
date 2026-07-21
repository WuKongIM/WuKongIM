package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

var (
	// ErrInvalidCapture reports missing or malformed capture fences.
	ErrInvalidCapture = errors.New("backup runtime: invalid capture")
	// ErrStaleCapture reports a source cut that does not match the requested logical partition.
	ErrStaleCapture = errors.New("backup runtime: stale capture")
)

// StreamDescriptor identifies one logical plaintext stream.
type StreamDescriptor = backupartifact.StreamDescriptor

// CaptureRequest fences one node-local logical partition capture.
type CaptureRequest struct {
	// JobID identifies the active cluster backup job.
	JobID string
	// BackupEpoch fences reports from older job incarnations.
	BackupEpoch uint64
	// HashSlot identifies the required logical partition.
	HashSlot uint16
	// ConfigFingerprint proves non-secret backup configuration agreement.
	ConfigFingerprint string
	// Kind identifies whether this layer is incremental, synthetic, or materialized.
	Kind backupartifact.RestorePointKind
	// BaseRestorePointID identifies the signed point used for an incremental layer.
	BaseRestorePointID string
}

// PartitionSource opens one consistency- and retention-pinned logical partition view.
type PartitionSource interface {
	// OpenPartition establishes a source session for request.
	OpenPartition(ctx context.Context, request CaptureRequest) (PartitionSession, error)
}

// PartitionSession owns one stable metadata view and committed message cut.
type PartitionSession interface {
	// Cut returns the committed boundary represented by both streams.
	Cut() backupartifact.PartitionCut
	// OpenMetadata opens the portable metadata stream.
	OpenMetadata(ctx context.Context) (io.ReadCloser, error)
	// OpenMessages opens the portable committed-message stream.
	OpenMessages(ctx context.Context) (io.ReadCloser, error)
	// Close releases snapshots, retention pins, and other source resources.
	Close() error
}

// StreamReplicator chunks, encrypts, and verifies a plaintext stream in both repositories.
type StreamReplicator interface {
	// Replicate returns immutable encrypted object references in key order.
	Replicate(ctx context.Context, descriptor StreamDescriptor, plaintext io.Reader) ([]backupartifact.ObjectEntry, error)
}

// PartitionManifestStore publishes one small immutable manifest in both repositories.
type PartitionManifestStore interface {
	// Put publishes body under key only after its checksum verifies in both repositories.
	Put(ctx context.Context, key, checksum string, body []byte) error
}

// WorkerOptions configures one node-local capture worker.
type WorkerOptions struct {
	// Source provides stable logical partition streams.
	Source PartitionSource
	// Replicator stores bounded encrypted chunks.
	Replicator StreamReplicator
	// Manifests publishes completed partition manifests.
	Manifests PartitionManifestStore
}

// Worker captures logical partitions without entering foreground write paths.
type Worker struct {
	source     PartitionSource
	replicator StreamReplicator
	manifests  PartitionManifestStore
}

// NewWorker creates a node-local capture worker.
func NewWorker(options WorkerOptions) (*Worker, error) {
	if options.Source == nil || options.Replicator == nil || options.Manifests == nil {
		return nil, fmt.Errorf("%w: worker dependencies are incomplete", ErrInvalidCapture)
	}
	return &Worker{source: options.Source, replicator: options.Replicator, manifests: options.Manifests}, nil
}

// Capture replicates both streams and publishes one partition manifest and bounded report.
func (w *Worker) Capture(ctx context.Context, request CaptureRequest) (backupcontract.PartitionReport, error) {
	if strings.TrimSpace(request.JobID) == "" || request.BackupEpoch == 0 || !validFingerprint(request.ConfigFingerprint) {
		return backupcontract.PartitionReport{}, ErrInvalidCapture
	}
	session, err := w.source.OpenPartition(ctx, request)
	if err != nil {
		return backupcontract.PartitionReport{}, err
	}
	if session == nil {
		return backupcontract.PartitionReport{}, fmt.Errorf("%w: source returned no session", ErrInvalidCapture)
	}
	defer session.Close()
	cut := session.Cut()
	if cut.HashSlot != request.HashSlot || cut.RaftIndex == 0 || cut.CommittedAtMillis <= 0 {
		return backupcontract.PartitionReport{}, ErrStaleCapture
	}

	metadata, err := w.replicateSessionStream(ctx, session.OpenMetadata, StreamDescriptor{JobID: request.JobID, HashSlot: request.HashSlot, Kind: backupartifact.ObjectKindMetadata})
	if err != nil {
		return backupcontract.PartitionReport{}, err
	}
	messages, err := w.replicateSessionStream(ctx, session.OpenMessages, StreamDescriptor{JobID: request.JobID, HashSlot: request.HashSlot, Kind: backupartifact.ObjectKindMessages})
	if err != nil {
		return backupcontract.PartitionReport{}, err
	}
	objects := append(metadata, messages...)
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
	manifest := backupartifact.PartitionManifest{
		Format:      backupartifact.PartitionManifestFormat,
		Version:     backupartifact.PartitionManifestVersion,
		JobID:       request.JobID,
		BackupEpoch: request.BackupEpoch,
		Cut:         cut,
		Objects:     objects,
	}
	body, err := backupartifact.MarshalPartitionManifest(manifest)
	if err != nil {
		return backupcontract.PartitionReport{}, err
	}
	hash := sha256.Sum256(body)
	checksum := hex.EncodeToString(hash[:])
	key := fmt.Sprintf("partition-manifests/%s/%05d.json", request.JobID, request.HashSlot)
	if err := w.manifests.Put(ctx, key, checksum, body); err != nil {
		return backupcontract.PartitionReport{}, err
	}
	var ciphertextBytes uint64
	for _, object := range objects {
		if object.CiphertextBytes <= 0 || uint64(object.CiphertextBytes) > math.MaxUint64-ciphertextBytes {
			return backupcontract.PartitionReport{}, fmt.Errorf("%w: partition ciphertext size overflow", ErrInvalidCapture)
		}
		ciphertextBytes += uint64(object.CiphertextBytes)
	}
	return backupcontract.PartitionReport{
		JobID:                 request.JobID,
		BackupEpoch:           request.BackupEpoch,
		HashSlot:              request.HashSlot,
		RaftIndex:             cut.RaftIndex,
		CommittedAtUnixMillis: cut.CommittedAtMillis,
		ManifestKey:           key,
		ManifestSHA256:        checksum,
		ObjectCount:           uint64(len(objects)),
		CiphertextBytes:       ciphertextBytes,
	}, nil
}

func (w *Worker) replicateSessionStream(ctx context.Context, open func(context.Context) (io.ReadCloser, error), descriptor StreamDescriptor) ([]backupartifact.ObjectEntry, error) {
	reader, err := open(ctx)
	if err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, fmt.Errorf("%w: source returned no stream", ErrInvalidCapture)
	}
	entries, replicateErr := w.replicator.Replicate(ctx, descriptor, reader)
	closeErr := reader.Close()
	if replicateErr != nil {
		return nil, replicateErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return entries, nil
}

func validFingerprint(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
