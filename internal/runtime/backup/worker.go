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
	// Generation identifies the continuous-capture generation being rebased.
	Generation string
	// RebaseEpoch fences reports from older generation-rebase attempts.
	RebaseEpoch uint64
	// HashSlot identifies the required logical partition.
	HashSlot uint16
	// ConfigFingerprint proves non-secret backup configuration agreement.
	ConfigFingerprint string
}

// MaterializedPartitionReport authenticates one completed rebase baseline.
// It is node-RPC evidence only and is never persisted as Controller task state.
type MaterializedPartitionReport struct {
	// Generation identifies the completed replacement Generation.
	Generation string
	// RebaseEpoch fences the completed immutable rebase attempt.
	RebaseEpoch uint64
	// HashSlot identifies the independently replaced logical partition.
	HashSlot uint16
	// RaftIndex is the exact committed source cut represented by the baseline.
	RaftIndex uint64
	// CommittedAtUnixMillis is the source cut's durable commit time.
	CommittedAtUnixMillis int64
	// ManifestKey is the immutable portable manifest object key.
	ManifestKey string
	// ManifestSHA256 authenticates the immutable manifest bytes.
	ManifestSHA256 string
	// ObjectCount is the bounded number of encrypted baseline objects.
	ObjectCount uint64
	// CiphertextBytes is the total encrypted baseline size.
	CiphertextBytes uint64
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
	// Evidence returns exact counts and allocator fences for both streams.
	Evidence() backupartifact.PartitionEvidence
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
	// Load returns a verified existing manifest and repairs a missing replica.
	Load(ctx context.Context, key string) (body []byte, checksum string, err error)
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
func (w *Worker) Capture(ctx context.Context, request CaptureRequest) (MaterializedPartitionReport, error) {
	if strings.TrimSpace(request.Generation) == "" ||
		request.RebaseEpoch == 0 ||
		!validFingerprint(request.ConfigFingerprint) {
		return MaterializedPartitionReport{}, ErrInvalidCapture
	}
	if report, ok, err := loadExistingPartitionReport(ctx, w.manifests, request); err != nil {
		return MaterializedPartitionReport{}, err
	} else if ok {
		return report, nil
	}
	session, err := w.source.OpenPartition(ctx, request)
	if err != nil {
		return MaterializedPartitionReport{}, err
	}
	if session == nil {
		return MaterializedPartitionReport{}, fmt.Errorf("%w: source returned no session", ErrInvalidCapture)
	}
	defer session.Close()
	cut := session.Cut()
	if cut.HashSlot != request.HashSlot || cut.RaftIndex == 0 || cut.CommittedAtMillis <= 0 {
		return MaterializedPartitionReport{}, ErrStaleCapture
	}

	metadata, err := w.replicateSessionStream(ctx, session.OpenMetadata, StreamDescriptor{Generation: request.Generation, HashSlot: request.HashSlot, Kind: backupartifact.ObjectKindMetadata})
	if err != nil {
		return MaterializedPartitionReport{}, err
	}
	messages, err := w.replicateSessionStream(ctx, session.OpenMessages, StreamDescriptor{Generation: request.Generation, HashSlot: request.HashSlot, Kind: backupartifact.ObjectKindMessages})
	if err != nil {
		return MaterializedPartitionReport{}, err
	}
	objects := append(metadata, messages...)
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
	evidence := session.Evidence()
	if evidence.Version != backupartifact.PartitionEvidenceVersion {
		return MaterializedPartitionReport{}, fmt.Errorf("%w: source returned invalid evidence", ErrInvalidCapture)
	}
	manifest := backupartifact.PartitionManifest{
		Format:      backupartifact.PartitionManifestFormat,
		Version:     backupartifact.PartitionManifestVersion,
		Generation:  request.Generation,
		RebaseEpoch: request.RebaseEpoch,
		Cut:         cut,
		Evidence:    evidence,
		Objects:     objects,
	}
	body, err := backupartifact.MarshalPartitionManifest(manifest)
	if err != nil {
		return MaterializedPartitionReport{}, err
	}
	hash := sha256.Sum256(body)
	checksum := hex.EncodeToString(hash[:])
	key := partitionManifestKey(request)
	if err := w.manifests.Put(ctx, key, checksum, body); err != nil {
		return MaterializedPartitionReport{}, err
	}
	return partitionReportFromManifest(key, checksum, manifest)
}

func partitionManifestKey(request CaptureRequest) string {
	return fmt.Sprintf(
		"partition-manifests/%s/%05d.json",
		request.Generation, request.HashSlot,
	)
}

func loadExistingPartitionReport(ctx context.Context, store PartitionManifestStore, request CaptureRequest) (MaterializedPartitionReport, bool, error) {
	_, checksum, manifest, ok, err := loadExistingPartitionManifest(ctx, store, request)
	if err != nil || !ok {
		return MaterializedPartitionReport{}, ok, err
	}
	report, err := partitionReportFromManifest(partitionManifestKey(request), checksum, manifest)
	return report, err == nil, err
}

func loadExistingPartitionManifest(
	ctx context.Context,
	store PartitionManifestStore,
	request CaptureRequest,
) ([]byte, string, backupartifact.PartitionManifest, bool, error) {
	key := partitionManifestKey(request)
	body, checksum, err := store.Load(ctx, key)
	if errors.Is(err, backupartifact.ErrObjectNotFound) {
		return nil, "", backupartifact.PartitionManifest{}, false, nil
	}
	if err != nil {
		return nil, "", backupartifact.PartitionManifest{}, false, err
	}
	hash := sha256.Sum256(body)
	if checksum != hex.EncodeToString(hash[:]) {
		return nil, "", backupartifact.PartitionManifest{}, false, fmt.Errorf("%w: existing partition manifest checksum mismatch", ErrInvalidCapture)
	}
	manifest, err := backupartifact.LoadPartitionManifest(body)
	if err != nil {
		return nil, "", backupartifact.PartitionManifest{}, false, err
	}
	if manifest.Generation != request.Generation ||
		manifest.RebaseEpoch != request.RebaseEpoch ||
		manifest.Cut.HashSlot != request.HashSlot {
		return nil, "", backupartifact.PartitionManifest{}, false, fmt.Errorf("%w: existing partition manifest fence mismatch", ErrStaleCapture)
	}
	return body, checksum, manifest, true, nil
}

func partitionReportFromManifest(key, checksum string, manifest backupartifact.PartitionManifest) (MaterializedPartitionReport, error) {
	var ciphertextBytes uint64
	for _, object := range manifest.Objects {
		if object.CiphertextBytes <= 0 || uint64(object.CiphertextBytes) > math.MaxUint64-ciphertextBytes {
			return MaterializedPartitionReport{}, fmt.Errorf("%w: partition ciphertext size overflow", ErrInvalidCapture)
		}
		ciphertextBytes += uint64(object.CiphertextBytes)
	}
	return MaterializedPartitionReport{
		Generation:            manifest.Generation,
		RebaseEpoch:           manifest.RebaseEpoch,
		HashSlot:              manifest.Cut.HashSlot,
		RaftIndex:             manifest.Cut.RaftIndex,
		CommittedAtUnixMillis: manifest.Cut.CommittedAtMillis,
		ManifestKey:           key,
		ManifestSHA256:        checksum,
		ObjectCount:           uint64(len(manifest.Objects)),
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
