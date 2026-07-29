package backup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// FullSlotStream is one portable metadata or message snapshot emitted by a
// stable Hash Slot capture. Multiple message streams are allowed.
type FullSlotStream struct {
	// Kind selects the portable metadata or message chunk format.
	Kind backupartifact.ChunkKind
	// Reader owns one bounded logical stream and must be closed by the exporter.
	Reader io.ReadCloser
	// Records is the exact logical record count covered by Reader.
	Records uint64
	// MaxMessageID is the highest durable message identifier in this stream.
	MaxMessageID uint64
}

// FullSlotCapture pins one stable Hash Slot cut and yields its logical streams
// in metadata-then-messages order.
type FullSlotCapture interface {
	Cut() backupartifact.SlotCut
	Next(context.Context) (FullSlotStream, error)
	Close() error
}

// FullSlotSource opens one online logical Hash Slot capture.
type FullSlotSource interface {
	OpenFullSlot(context.Context, uint16) (FullSlotCapture, error)
}

// FullExporterOptions configures one node-local bounded full-backup exporter.
type FullExporterOptions struct {
	// Store is the single repository receiving immutable attempt objects.
	Store backupartifact.ArchiveStore
	// Source opens authority-fenced online Hash Slot snapshots.
	Source FullSlotSource
	// TempDir holds bounded compression buffers outside active product data.
	TempDir string
}

// FullExporter writes resumable per-Slot artifacts. It never publishes the
// archive-level COMPLETE marker.
type FullExporter struct {
	store   backupartifact.ArchiveStore
	source  FullSlotSource
	tempDir string
	streams *FullStreamWriter
}

// NewFullExporter validates exporter dependencies.
func NewFullExporter(options FullExporterOptions) (*FullExporter, error) {
	if options.Store == nil || options.Source == nil || options.TempDir == "" {
		return nil, fmt.Errorf("backup runtime: full exporter dependencies are required")
	}
	return &FullExporter{
		store: options.Store, source: options.Source, tempDir: options.TempDir,
		streams: &FullStreamWriter{
			store: options.Store, tempDir: options.TempDir,
		},
	}, nil
}

// FullStreamWriterOptions configures direct node-local chunk publication.
type FullStreamWriterOptions struct {
	// Store is the repository receiving immutable compressed chunks.
	Store backupartifact.ArchiveStore
	// TempDir holds one bounded chunk buffer per active writer.
	TempDir string
}

// FullStreamWriter stores one bounded logical snapshot as immutable chunks.
// It is shared by Slot leaders and remote Channel leaders so large message
// streams never cross cluster RPC payloads.
type FullStreamWriter struct {
	store   backupartifact.ArchiveStore
	tempDir string
}

// NewFullStreamWriter creates a direct archive stream writer.
func NewFullStreamWriter(
	options FullStreamWriterOptions,
) (*FullStreamWriter, error) {
	if options.Store == nil || options.TempDir == "" {
		return nil, fmt.Errorf("backup runtime: full stream writer dependencies are required")
	}
	return &FullStreamWriter{
		store: options.Store, tempDir: options.TempDir,
	}, nil
}

// Write stores one metadata or message stream. firstSequence and streamNumber
// are supplied by the Slot leader so every returned reference is deterministic.
func (w *FullStreamWriter) Write(
	ctx context.Context,
	backupID string,
	hashSlot uint16,
	stream FullSlotStream,
	firstSequence uint32,
	streamNumber uint32,
) ([]backupartifact.ChunkReference, error) {
	return w.WriteAt(
		ctx, backupID, hashSlot,
		fmt.Sprintf("slots/%03d", hashSlot),
		stream, firstSequence, streamNumber,
	)
}

// WriteAt stores a stream below one immutable attempt-scoped Slot prefix.
func (w *FullStreamWriter) WriteAt(
	ctx context.Context,
	backupID string,
	hashSlot uint16,
	artifactPrefix string,
	stream FullSlotStream,
	firstSequence uint32,
	streamNumber uint32,
) ([]backupartifact.ChunkReference, error) {
	if w == nil || w.store == nil || w.tempDir == "" ||
		firstSequence == 0 || stream.Reader == nil ||
		(stream.Kind != backupartifact.ChunkKindMetadata &&
			stream.Kind != backupartifact.ChunkKindMessages) {
		if stream.Reader != nil {
			_ = stream.Reader.Close()
		}
		return nil, fmt.Errorf("backup runtime: invalid full stream")
	}
	if err := validateFullExportTarget(backupID, hashSlot); err != nil {
		_ = stream.Reader.Close()
		return nil, err
	}
	requiredPrefix := fmt.Sprintf("slots/%03d", hashSlot)
	if artifactPrefix != requiredPrefix &&
		!strings.HasPrefix(artifactPrefix, requiredPrefix+"/attempts/") {
		_ = stream.Reader.Close()
		return nil, fmt.Errorf("backup runtime: invalid Slot artifact prefix")
	}
	if err := backupartifact.ValidateRepositoryKey(artifactPrefix); err != nil {
		_ = stream.Reader.Close()
		return nil, fmt.Errorf("backup runtime: invalid Slot artifact prefix")
	}
	references, writeErr := w.write(
		ctx, backupID, artifactPrefix, stream, firstSequence, streamNumber,
	)
	return references, errors.Join(writeErr, stream.Reader.Close())
}

// ExportSlot captures and replaces one incomplete backup's Hash Slot subtree,
// returning the immutable reference needed by the archive manifest.
func (e *FullExporter) ExportSlot(
	ctx context.Context,
	backupID string,
	hashSlot uint16,
) (backupartifact.SlotReference, error) {
	if err := validateFullExportTarget(backupID, hashSlot); err != nil {
		return backupartifact.SlotReference{}, err
	}
	slotRoot := fmt.Sprintf("backups/%s/slots/%03d", backupID, hashSlot)
	if err := e.store.DeletePrefix(ctx, slotRoot); err != nil {
		return backupartifact.SlotReference{}, err
	}
	capture, err := e.source.OpenFullSlot(ctx, hashSlot)
	if err != nil {
		return backupartifact.SlotReference{}, err
	}
	defer capture.Close()
	cut := capture.Cut()
	if cut.PhysicalSlotID == 0 || cut.LeaderTerm == 0 ||
		cut.AppliedTerm == 0 ||
		cut.ConfigurationVersion == 0 || cut.AppliedIndex == 0 ||
		cut.CapturedAtUnixMillis <= 0 {
		return backupartifact.SlotReference{}, fmt.Errorf("backup runtime: invalid Hash Slot cut")
	}

	manifest := backupartifact.SlotManifest{
		Format:   backupartifact.SlotManifestFormat,
		Version:  backupartifact.SlotManifestVersion,
		HashSlot: hashSlot,
		Cut:      cut,
		Chunks:   []backupartifact.ChunkReference{},
	}
	nextSequence := map[backupartifact.ChunkKind]uint32{
		backupartifact.ChunkKindMetadata: 1,
		backupartifact.ChunkKindMessages: 1,
	}
	nextStream := map[backupartifact.ChunkKind]uint32{
		backupartifact.ChunkKindMetadata: 0,
		backupartifact.ChunkKindMessages: 1,
	}
	seenMetadata := false
	for {
		stream, nextErr := capture.Next(ctx)
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return backupartifact.SlotReference{}, nextErr
		}
		if stream.Reader == nil ||
			(stream.Kind != backupartifact.ChunkKindMetadata &&
				stream.Kind != backupartifact.ChunkKindMessages) ||
			(stream.Kind == backupartifact.ChunkKindMetadata && seenMetadata) ||
			(stream.Kind == backupartifact.ChunkKindMessages && !seenMetadata) {
			if stream.Reader != nil {
				_ = stream.Reader.Close()
			}
			return backupartifact.SlotReference{}, fmt.Errorf("backup runtime: invalid Slot stream order")
		}
		if stream.Kind == backupartifact.ChunkKindMetadata {
			seenMetadata = true
		}
		references, streamErr := e.streams.Write(
			ctx, backupID, hashSlot, stream,
			nextSequence[stream.Kind], nextStream[stream.Kind],
		)
		if streamErr != nil {
			return backupartifact.SlotReference{}, streamErr
		}
		manifest.Chunks = append(manifest.Chunks, references...)
		nextSequence[stream.Kind] += uint32(len(references))
		nextStream[stream.Kind]++
	}
	if !seenMetadata {
		return backupartifact.SlotReference{}, fmt.Errorf("backup runtime: metadata stream is required")
	}
	for _, chunk := range manifest.Chunks {
		manifest.LogicalBytes += chunk.Descriptor.LogicalBytes
		manifest.StoredBytes += chunk.Descriptor.StoredBytes
		manifest.Records += chunk.Records
		if chunk.MaxMessageID > manifest.MaxMessageID {
			manifest.MaxMessageID = chunk.MaxMessageID
		}
	}
	body, err := backupartifact.MarshalSlotManifest(manifest)
	if err != nil {
		return backupartifact.SlotReference{}, err
	}
	manifestKey := fmt.Sprintf("slots/%03d/manifest.json", hashSlot)
	if err := e.store.Put(ctx, backupartifact.PutObject{
		Key:           "backups/" + backupID + "/" + manifestKey,
		Body:          bytes.NewReader(body),
		ExpectedBytes: uint64(len(body)),
	}); err != nil {
		return backupartifact.SlotReference{}, err
	}
	sum := sha256.Sum256(body)
	return backupartifact.SlotReference{
		HashSlot:       hashSlot,
		ManifestKey:    manifestKey,
		ManifestSHA256: hex.EncodeToString(sum[:]),
		LogicalBytes:   manifest.LogicalBytes,
		StoredBytes:    manifest.StoredBytes,
		Records:        manifest.Records,
		MaxMessageID:   manifest.MaxMessageID,
	}, nil
}

func (w *FullStreamWriter) write(
	ctx context.Context,
	backupID string,
	artifactPrefix string,
	stream FullSlotStream,
	firstSequence uint32,
	streamNumber uint32,
) ([]backupartifact.ChunkReference, error) {
	buffered := bufio.NewReaderSize(stream.Reader, 64<<10)
	references := make([]backupartifact.ChunkReference, 0, 1)
	for part := uint32(1); ; part++ {
		temporary, err := os.CreateTemp(w.tempDir, "wukongim-backup-chunk-*")
		if err != nil {
			return nil, err
		}
		tempPath := temporary.Name()
		limited := &io.LimitedReader{R: buffered, N: int64(backupartifact.MaxChunkLogicalBytes)}
		descriptor, encodeErr := backupartifact.EncodeChunk(temporary, limited)
		closeErr := temporary.Close()
		if encodeErr != nil || closeErr != nil {
			_ = os.Remove(tempPath)
			return nil, errors.Join(encodeErr, closeErr)
		}
		_, peekErr := buffered.Peek(1)
		final := errors.Is(peekErr, io.EOF)
		if peekErr != nil && !final {
			_ = os.Remove(tempPath)
			return nil, peekErr
		}
		sequence := firstSequence + uint32(len(references))
		name := "meta"
		if stream.Kind == backupartifact.ChunkKindMessages {
			name = "messages"
		}
		relativeKey := fmt.Sprintf(
			"%s/%s-%06d.zst", artifactPrefix, name, sequence,
		)
		file, err := os.Open(tempPath)
		if err != nil {
			_ = os.Remove(tempPath)
			return nil, err
		}
		putErr := w.store.Put(ctx, backupartifact.PutObject{
			Key:           "backups/" + backupID + "/" + relativeKey,
			Body:          file,
			ExpectedBytes: descriptor.StoredBytes,
			IfAbsent:      true,
		})
		fileCloseErr := file.Close()
		removeErr := os.Remove(tempPath)
		if putErr != nil || fileCloseErr != nil || removeErr != nil {
			return nil, errors.Join(putErr, fileCloseErr, removeErr)
		}
		reference := backupartifact.ChunkReference{
			Kind:       stream.Kind,
			Sequence:   sequence,
			Stream:     streamNumber,
			Part:       part,
			Final:      final,
			Key:        relativeKey,
			Descriptor: descriptor,
		}
		if part == 1 {
			reference.Records = stream.Records
			reference.MaxMessageID = stream.MaxMessageID
		}
		references = append(references, reference)
		if final {
			return references, nil
		}
	}
}

func validateFullExportTarget(backupID string, hashSlot uint16) error {
	if int(hashSlot) >= backupartifact.DefaultHashSlotCount {
		return fmt.Errorf("backup runtime: Hash Slot is out of range")
	}
	key := "backups/" + backupID + "/manifest.json"
	if err := backupartifact.ValidateRepositoryKey(key); err != nil ||
		backupID == "" || filepath.Base(backupID) != backupID {
		return fmt.Errorf("backup runtime: invalid backup ID")
	}
	return nil
}
