package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"io"
	"sync"
	"testing"
	"time"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestPermanentErasureLedgerPublishesEncryptedSignedDualRepositoryCommit(t *testing.T) {
	t.Parallel()

	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	seed := sha256.Sum256([]byte("erasure-ledger-test"))
	signer := testEd25519Signer{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	store := &erasureLedgerStateStore{}
	coordinator, err := backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: 256, Store: store, Publisher: erasureLedgerNoopPublisher{},
		Now: func() time.Time { return time.UnixMilli(1_753_056_360_000).UTC() }, NewJobID: func() string { return "unused" },
	})
	require.NoError(t, err)
	codec := backupartifact.NewObjectCodec(testWrappingKeyManager{mask: 0x5a}, bytes.NewReader(bytes.Repeat([]byte{0x44}, 256)))
	ledger, err := backupinfra.NewPermanentErasureLedger(backupinfra.PermanentErasureLedgerOptions{
		Primary: primary, Secondary: secondary, Codec: codec, Coordinator: coordinator,
		Signer: signer, SigningKeyID: "signing-key", KMSKeyID: "kms-key",
		RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: "generation-1", HashSlotCount: 256,
		Now: func() time.Time { return time.UnixMilli(1_753_056_360_000).UTC() }, NewAttemptID: func() string { return "attempt-1" },
	})
	require.NoError(t, err)

	receipt, err := ledger.RecordPermanentMessageErasure(context.Background(), backupinfra.PermanentMessageErasure{
		ChannelID: "channel-a", ChannelType: 2, ThroughSeq: 41, RequestedAtUnixMillis: 1_753_056_359_000,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), receipt.Sequence)
	require.Len(t, receipt.EventID, 64)

	commitKey := backupartifact.ErasureLedgerCommitKey(receipt.Sequence)
	primaryCommitBody := readRepositoryBody(t, primary, commitKey)
	secondaryCommitBody := readRepositoryBody(t, secondary, commitKey)
	require.Equal(t, primaryCommitBody, secondaryCommitBody)
	commit, err := backupartifact.LoadErasureLedgerCommit(context.Background(), primaryCommitBody, signer)
	require.NoError(t, err)
	require.Equal(t, receipt.EventID, commit.EventID)

	primaryRecordBody := readRepositoryBody(t, primary, commit.RecordKey)
	require.Equal(t, primaryRecordBody, readRepositoryBody(t, secondary, commit.RecordKey))
	record, err := backupartifact.LoadErasureLedgerRecord(context.Background(), primaryRecordBody, signer)
	require.NoError(t, err)
	require.NotContains(t, string(primaryRecordBody), "channel-a")
	ciphertext := readRepositoryBody(t, primary, record.Object.Key)
	require.Equal(t, ciphertext, readRepositoryBody(t, secondary, record.Object.Key))
	plaintext, err := codec.Open(context.Background(), record.Object, ciphertext)
	require.NoError(t, err)
	event, err := backupartifact.LoadErasureLedgerEvent(plaintext)
	require.NoError(t, err)
	require.Equal(t, "channel-a", event.ChannelID)
	require.Equal(t, uint64(41), event.ThroughSeq)
	loader, err := backupinfra.NewErasureLedgerLoader(backupinfra.ErasureLedgerLoaderOptions{
		Primary: primary, Secondary: secondary, Signer: signer, Codec: codec,
		RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: "generation-1", HashSlotCount: 256,
	})
	require.NoError(t, err)
	snapshot, err := loader.LoadDualSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), snapshot.Boundary)
	require.NotEqual(t, backupartifact.EmptyErasureLedgerSnapshotSHA256, snapshot.SHA256)
	require.Equal(t, []backupinfra.PermanentErasureBoundary{{ChannelID: "channel-a", ChannelType: 2, ThroughSeq: 41}}, snapshot.Boundaries(event.HashSlot))
	pinned, err := loader.LoadPinnedSnapshot(context.Background(), "secondary", snapshot.Version, snapshot.Boundary, snapshot.SHA256)
	require.NoError(t, err)
	require.Equal(t, snapshot.SHA256, pinned.SHA256)

	retry, err := ledger.RecordPermanentMessageErasure(context.Background(), backupinfra.PermanentMessageErasure{
		ChannelID: "channel-a", ChannelType: 2, ThroughSeq: 41, RequestedAtUnixMillis: 1_753_056_359_000,
	})
	require.NoError(t, err)
	require.Equal(t, receipt, retry)
	_, err = primary.Stat(context.Background(), backupartifact.ErasureLedgerCommitKey(2))
	require.ErrorIs(t, err, backupartifact.ErrObjectNotFound)

	second, err := ledger.RecordPermanentMessageErasure(context.Background(), backupinfra.PermanentMessageErasure{
		ChannelID: "channel-b", ChannelType: 2, ThroughSeq: 9, RequestedAtUnixMillis: 1_753_056_360_000,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(2), second.Sequence)
	oldRetry, err := ledger.RecordPermanentMessageErasure(context.Background(), backupinfra.PermanentMessageErasure{
		ChannelID: "channel-a", ChannelType: 2, ThroughSeq: 41, RequestedAtUnixMillis: 1_753_056_359_000,
	})
	require.NoError(t, err)
	require.Equal(t, receipt, oldRetry)
	_, err = primary.Stat(context.Background(), backupartifact.ErasureLedgerCommitKey(3))
	require.ErrorIs(t, err, backupartifact.ErrObjectNotFound)
}

func readRepositoryBody(t *testing.T, repository backupartifact.Repository, key string) []byte {
	t.Helper()
	reader, _, err := repository.Open(context.Background(), key)
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	return body
}

type erasureLedgerStateStore struct {
	mu    sync.Mutex
	state backupusecase.State
}

func (s *erasureLedgerStateStore) Load(context.Context) (backupusecase.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Clone(), nil
}

func (s *erasureLedgerStateStore) CompareAndSwap(_ context.Context, revision uint64, next backupusecase.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Revision != revision {
		return backupusecase.ErrStateConflict
	}
	next.Revision = revision + 1
	s.state = next.Clone()
	return nil
}

type erasureLedgerNoopPublisher struct{}

func (erasureLedgerNoopPublisher) Publish(context.Context, backupusecase.Job) (backupusecase.RestorePoint, error) {
	return backupusecase.RestorePoint{}, nil
}
