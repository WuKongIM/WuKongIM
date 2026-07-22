package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const (
	defaultBackupChunkBytes  = 8 << 20
	maxBackupChunkBytes      = 256 << 20
	maxBackupChunksPerStream = 1 << 20
)

// ChunkReplicatorOptions configures bounded compress-encrypt-upload work.
type ChunkReplicatorOptions struct {
	// Codec compresses and envelope-encrypts each bounded plaintext chunk.
	Codec *backupartifact.ObjectCodec
	// Publisher writes each sealed chunk to both explicit repositories.
	Publisher *backupartifact.ReplicatedPublisher
	// KMSKeyID identifies the external key-encryption key.
	KMSKeyID string
	// ChunkBytes bounds plaintext memory per object. Zero defaults to eight MiB.
	ChunkBytes int
}

// StreamDescriptor identifies one logical plaintext stream.
type StreamDescriptor = backupartifact.StreamDescriptor

// ChunkReplicator splits a stream into bounded independently encrypted immutable objects.
type ChunkReplicator struct {
	codec      *backupartifact.ObjectCodec
	publisher  *backupartifact.ReplicatedPublisher
	kmsKeyID   string
	chunkBytes int
}

// NewChunkReplicator creates a bounded stream replicator.
func NewChunkReplicator(options ChunkReplicatorOptions) (*ChunkReplicator, error) {
	chunkBytes := options.ChunkBytes
	if chunkBytes == 0 {
		chunkBytes = defaultBackupChunkBytes
	}
	if options.Codec == nil || options.Publisher == nil || strings.TrimSpace(options.KMSKeyID) == "" || chunkBytes <= 0 || chunkBytes > maxBackupChunkBytes {
		return nil, fmt.Errorf("backup chunk replicator: invalid options")
	}
	return &ChunkReplicator{
		codec:      options.Codec,
		publisher:  options.Publisher,
		kmsKeyID:   options.KMSKeyID,
		chunkBytes: chunkBytes,
	}, nil
}

// Replicate reads plaintext incrementally and returns verified object references in key order.
func (r *ChunkReplicator) Replicate(ctx context.Context, descriptor StreamDescriptor, plaintext io.Reader) ([]backupartifact.ObjectEntry, error) {
	if r == nil || plaintext == nil || !safeJobID(descriptor.JobID) || !validStreamKind(descriptor.Kind) || !safeStreamShardID(descriptor.ShardID) {
		return nil, fmt.Errorf("%w: invalid stream descriptor", backupartifact.ErrInvalidObject)
	}
	buffer := make([]byte, r.chunkBytes)
	entries := make([]backupartifact.ObjectEntry, 0, 1)
	var attemptBytes [12]byte
	if _, err := rand.Read(attemptBytes[:]); err != nil {
		return nil, fmt.Errorf("backup chunk replicator: create attempt namespace: %w", err)
	}
	attemptNamespace := descriptor.JobID + "-" + hex.EncodeToString(attemptBytes[:])
	for ordinal := 0; ; ordinal++ {
		if ordinal >= maxBackupChunksPerStream {
			return nil, fmt.Errorf("%w: stream chunk count exceeds limit", backupartifact.ErrInvalidObject)
		}
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		readBytes, readErr := io.ReadFull(plaintext, buffer)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return nil, readErr
		}
		if readBytes == 0 && readErr == io.EOF && ordinal > 0 {
			break
		}
		streamName := string(descriptor.Kind)
		if descriptor.ShardID != "" {
			streamName += "-" + descriptor.ShardID
		}
		key := fmt.Sprintf("objects/%s/%05d/%s-%06d.bin", attemptNamespace, descriptor.HashSlot, streamName, ordinal)
		sealed, err := r.codec.Seal(ctx, backupartifact.ObjectDescriptor{
			Key:      key,
			Kind:     descriptor.Kind,
			HashSlot: descriptor.HashSlot,
			KMSKeyID: r.kmsKeyID,
		}, buffer[:readBytes])
		if err != nil {
			return nil, err
		}
		if err := r.publisher.ReplicateObject(ctx, sealed); err != nil {
			return nil, err
		}
		entries = append(entries, sealed.Entry)
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
	}
	return entries, nil
}

func safeStreamShardID(value string) bool {
	if value == "" {
		return true
	}
	return safeJobID(value) && len(value) <= 64
}

func safeJobID(value string) bool {
	if value == "" || len(value) > 128 || strings.Contains(value, "..") {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || (char == '.' && index > 0) {
			continue
		}
		return false
	}
	return true
}

func validStreamKind(kind backupartifact.ObjectKind) bool {
	return kind == backupartifact.ObjectKindMetadata || kind == backupartifact.ObjectKindMessages || kind == backupartifact.ObjectKindErasureLedger || kind == backupartifact.ObjectKindChannelIndex
}
