package backup_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestChunkReplicatorBoundsAndRestoresStream(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	keys := testWrappingKeyManager{mask: 0x5a}
	codec := backupartifact.NewObjectCodec(keys, bytes.NewReader(bytes.Repeat([]byte{0x33}, 128)))
	replicator, err := backupinfra.NewChunkReplicator(backupinfra.ChunkReplicatorOptions{
		Codec:      codec,
		Publisher:  backupartifact.NewReplicatedPublisher(primary, secondary),
		KMSKeyID:   "kms-backup",
		ChunkBytes: 4,
	})
	require.NoError(t, err)

	entries, err := replicator.Replicate(context.Background(), backupinfra.StreamDescriptor{
		JobID:    "backup-9",
		HashSlot: 7,
		Kind:     backupartifact.ObjectKindMetadata,
	}, bytes.NewReader([]byte("abcdefghij")))
	require.NoError(t, err)
	require.Len(t, entries, 3)

	var restored []byte
	for _, entry := range entries {
		reader, _, err := secondary.Open(context.Background(), entry.Key)
		require.NoError(t, err)
		ciphertext, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		plaintext, err := codec.Open(context.Background(), entry, ciphertext)
		require.NoError(t, err)
		restored = append(restored, plaintext...)
	}
	require.Equal(t, []byte("abcdefghij"), restored)
}

func TestChunkReplicatorUsesShardIDToAvoidMessageKeyCollisions(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	codec := backupartifact.NewObjectCodec(testWrappingKeyManager{mask: 0x5a}, bytes.NewReader(bytes.Repeat([]byte{0x44}, 128)))
	replicator, err := backupinfra.NewChunkReplicator(backupinfra.ChunkReplicatorOptions{Codec: codec, Publisher: backupartifact.NewReplicatedPublisher(primary, secondary), KMSKeyID: "kms-backup", ChunkBytes: 16})
	require.NoError(t, err)
	first, err := replicator.Replicate(context.Background(), backupinfra.StreamDescriptor{JobID: "backup-shards", HashSlot: 2, Kind: backupartifact.ObjectKindMessages, ShardID: "n1-0000"}, bytes.NewReader([]byte("one")))
	require.NoError(t, err)
	second, err := replicator.Replicate(context.Background(), backupinfra.StreamDescriptor{JobID: "backup-shards", HashSlot: 2, Kind: backupartifact.ObjectKindMessages, ShardID: "n2-0000"}, bytes.NewReader([]byte("two")))
	require.NoError(t, err)
	require.NotEqual(t, first[0].Key, second[0].Key)
}

func TestChunkReplicatorRetryAfterPartialUploadUsesFreshImmutableNamespace(t *testing.T) {
	primaryFile, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondaryFile, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	primary := &recordingRepository{Repository: primaryFile}
	secondary := &failOncePutRepository{Repository: secondaryFile, remaining: 1}
	codec := backupartifact.NewObjectCodec(testWrappingKeyManager{mask: 0x5a}, bytes.NewReader(bytes.Repeat([]byte{0x55}, 256)))
	replicator, err := backupinfra.NewChunkReplicator(backupinfra.ChunkReplicatorOptions{Codec: codec, Publisher: backupartifact.NewReplicatedPublisher(primary, secondary), KMSKeyID: "kms-backup", ChunkBytes: 16})
	require.NoError(t, err)
	descriptor := backupinfra.StreamDescriptor{JobID: "backup-retry", HashSlot: 2, Kind: backupartifact.ObjectKindMetadata}
	_, err = replicator.Replicate(context.Background(), descriptor, bytes.NewReader([]byte("same stream")))
	require.Error(t, err)
	second, err := replicator.Replicate(context.Background(), descriptor, bytes.NewReader([]byte("same stream")))
	require.NoError(t, err)
	require.Len(t, primary.putKeys, 2)
	require.NotEqual(t, primary.putKeys[0], primary.putKeys[1])
	require.Equal(t, primary.putKeys[1], second[0].Key)
}

type recordingRepository struct {
	backupartifact.Repository
	putKeys []string
}

func (r *recordingRepository) PutImmutable(ctx context.Context, key string, size int64, checksum string, body io.Reader) error {
	r.putKeys = append(r.putKeys, key)
	return r.Repository.PutImmutable(ctx, key, size, checksum, body)
}

type failOncePutRepository struct {
	backupartifact.Repository
	remaining int
}

func (r *failOncePutRepository) PutImmutable(ctx context.Context, key string, size int64, checksum string, body io.Reader) error {
	if r.remaining > 0 {
		r.remaining--
		return io.ErrUnexpectedEOF
	}
	return r.Repository.PutImmutable(ctx, key, size, checksum, body)
}

type testWrappingKeyManager struct {
	mask byte
}

func (m testWrappingKeyManager) GenerateDataKey(context.Context, string) (backupartifact.DataKey, error) {
	plaintext := bytes.Repeat([]byte{0x61}, 32)
	return backupartifact.DataKey{Plaintext: plaintext, Wrapped: xorTestBytes(plaintext, m.mask)}, nil
}

func (m testWrappingKeyManager) UnwrapDataKey(_ context.Context, _ string, wrapped []byte) ([]byte, error) {
	return xorTestBytes(wrapped, m.mask), nil
}

func xorTestBytes(value []byte, mask byte) []byte {
	result := make([]byte, len(value))
	for index := range value {
		result[index] = value[index] ^ mask
	}
	return result
}
