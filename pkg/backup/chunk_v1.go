package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"

	"github.com/klauspost/compress/zstd"
)

const (
	// MaxChunkLogicalBytes bounds one uncompressed archive part.
	MaxChunkLogicalBytes uint64 = 64 << 20
)

// ChunkDescriptor authenticates one compressed archive part.
type ChunkDescriptor struct {
	StoredSHA256  string      `json:"stored_sha256"`
	LogicalSHA256 string      `json:"logical_sha256"`
	LogicalBytes  uint64      `json:"logical_bytes"`
	StoredBytes   uint64      `json:"stored_bytes"`
	Compression   Compression `json:"compression"`
}

// EncodeChunk streams one logical archive part through fixed Zstandard
// compression and records both stored and logical digests.
func EncodeChunk(dst io.Writer, src io.Reader) (ChunkDescriptor, error) {
	if dst == nil || src == nil {
		return ChunkDescriptor{}, fmt.Errorf("%w: chunk streams are required", ErrInvalidObject)
	}
	storedHash := sha256.New()
	storedCounter := &hashCountingWriter{dst: io.MultiWriter(dst, storedHash)}
	encoder, err := zstd.NewWriter(storedCounter, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return ChunkDescriptor{}, fmt.Errorf("backup: create chunk encoder: %w", err)
	}
	logicalHash := sha256.New()
	logicalCounter := &hashCountingReader{src: io.TeeReader(io.LimitReader(src, int64(MaxChunkLogicalBytes)+1), logicalHash)}
	_, copyErr := io.Copy(encoder, logicalCounter)
	closeErr := encoder.Close()
	if copyErr != nil {
		return ChunkDescriptor{}, fmt.Errorf("backup: encode chunk: %w", copyErr)
	}
	if closeErr != nil {
		return ChunkDescriptor{}, fmt.Errorf("backup: close chunk encoder: %w", closeErr)
	}
	if logicalCounter.count > MaxChunkLogicalBytes {
		return ChunkDescriptor{}, fmt.Errorf("%w: chunk exceeds %d logical bytes", ErrInvalidObject, MaxChunkLogicalBytes)
	}
	return ChunkDescriptor{
		StoredSHA256:  hex.EncodeToString(storedHash.Sum(nil)),
		LogicalSHA256: hex.EncodeToString(logicalHash.Sum(nil)),
		LogicalBytes:  logicalCounter.count,
		StoredBytes:   storedCounter.count,
		Compression:   CompressionZstd,
	}, nil
}

// DecodeChunk verifies and expands one compressed archive part.
func DecodeChunk(dst io.Writer, src io.Reader, descriptor ChunkDescriptor) error {
	if dst == nil || src == nil {
		return fmt.Errorf("%w: chunk streams are required", ErrInvalidObject)
	}
	if err := validateChunkDescriptor(descriptor); err != nil {
		return err
	}
	storedHash := sha256.New()
	storedCounter := &hashCountingReader{src: io.TeeReader(src, storedHash)}
	decoder, err := zstd.NewReader(
		storedCounter,
		zstd.WithDecoderMaxMemory(MaxChunkLogicalBytes*2),
	)
	if err != nil {
		return fmt.Errorf("%w: open chunk: %v", ErrObjectCorrupt, err)
	}
	logicalHash := sha256.New()
	logicalCounter := &hashCountingWriter{dst: io.MultiWriter(dst, logicalHash)}
	_, copyErr := io.Copy(logicalCounter, io.LimitReader(decoder, int64(MaxChunkLogicalBytes)+1))
	decoder.Close()
	if copyErr != nil {
		return fmt.Errorf("%w: decode chunk: %v", ErrObjectCorrupt, copyErr)
	}
	if storedCounter.count != descriptor.StoredBytes ||
		hex.EncodeToString(storedHash.Sum(nil)) != descriptor.StoredSHA256 ||
		logicalCounter.count != descriptor.LogicalBytes ||
		hex.EncodeToString(logicalHash.Sum(nil)) != descriptor.LogicalSHA256 {
		return fmt.Errorf("%w: chunk digest or size mismatch", ErrObjectCorrupt)
	}
	return nil
}

func validateChunkDescriptor(descriptor ChunkDescriptor) error {
	if descriptor.Compression != CompressionZstd ||
		descriptor.LogicalBytes > MaxChunkLogicalBytes ||
		descriptor.StoredBytes == 0 {
		return fmt.Errorf("%w: chunk descriptor", ErrInvalidObject)
	}
	if err := validateSHA256(descriptor.StoredSHA256); err != nil {
		return fmt.Errorf("%w: stored digest: %v", ErrInvalidObject, err)
	}
	if err := validateSHA256(descriptor.LogicalSHA256); err != nil {
		return fmt.Errorf("%w: logical digest: %v", ErrInvalidObject, err)
	}
	return nil
}

type hashCountingWriter struct {
	dst   io.Writer
	count uint64
	hash  hash.Hash
}

func (w *hashCountingWriter) Write(body []byte) (int, error) {
	written, err := w.dst.Write(body)
	w.count += uint64(written)
	if w.hash != nil && written > 0 {
		_, _ = w.hash.Write(body[:written])
	}
	return written, err
}

type hashCountingReader struct {
	src   io.Reader
	count uint64
}

func (r *hashCountingReader) Read(body []byte) (int, error) {
	read, err := r.src.Read(body)
	r.count += uint64(read)
	return read, err
}
