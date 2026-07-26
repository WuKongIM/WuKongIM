package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
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
		Enabled: true, HashSlotCount: 256, Store: store,
		Now: func() time.Time { return time.UnixMilli(1_753_056_360_000).UTC() },
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

	commitKey := backupartifact.ErasureLedgerCommitKey(erasureLedgerTestNamespace(), receipt.HashSlot, receipt.Sequence)
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
	require.Equal(t, uint64(1), snapshot.EventCount)
	require.NotEqual(t, backupartifact.EmptyErasureLedgerSnapshotSHA256, snapshot.SHA256)
	require.Equal(t, []backupinfra.PermanentErasureBoundary{{ChannelID: "channel-a", ChannelType: 2, ThroughSeq: 41}}, snapshot.Boundaries(event.HashSlot))
	pinned, err := loader.LoadPinnedSnapshot(context.Background(), "secondary", snapshot.Version, snapshot.EventCount, snapshot.SHA256, snapshot.Heads)
	require.NoError(t, err)
	require.Equal(t, snapshot.SHA256, pinned.SHA256)

	retry, err := ledger.RecordPermanentMessageErasure(context.Background(), backupinfra.PermanentMessageErasure{
		ChannelID: "channel-a", ChannelType: 2, ThroughSeq: 41, RequestedAtUnixMillis: 1_753_056_359_000,
	})
	require.NoError(t, err)
	require.Equal(t, receipt, retry)
	_, err = primary.Stat(context.Background(), backupartifact.ErasureLedgerCommitKey(erasureLedgerTestNamespace(), receipt.HashSlot, 2))
	require.ErrorIs(t, err, backupartifact.ErrObjectNotFound)

	channelB := channelInHashSlot(t, receipt.HashSlot, 256, "channel-b")
	second, err := ledger.RecordPermanentMessageErasure(context.Background(), backupinfra.PermanentMessageErasure{
		ChannelID: channelB, ChannelType: 2, ThroughSeq: 9, RequestedAtUnixMillis: 1_753_056_360_000,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(2), second.Sequence)
	oldRetry, err := ledger.RecordPermanentMessageErasure(context.Background(), backupinfra.PermanentMessageErasure{
		ChannelID: "channel-a", ChannelType: 2, ThroughSeq: 41, RequestedAtUnixMillis: 1_753_056_359_000,
	})
	require.NoError(t, err)
	require.Equal(t, receipt, oldRetry)
	_, err = primary.Stat(context.Background(), backupartifact.ErasureLedgerCommitKey(erasureLedgerTestNamespace(), receipt.HashSlot, 3))
	require.ErrorIs(t, err, backupartifact.ErrObjectNotFound)

	channels := []string{
		channelInHashSlot(t, receipt.HashSlot, 256, "concurrent-c"),
		channelInHashSlot(t, receipt.HashSlot, 256, "concurrent-d"),
	}
	results := make(chan backupinfra.ErasureLedgerReceipt, len(channels))
	failures := make(chan error, len(channels))
	var group sync.WaitGroup
	for index, channelID := range channels {
		group.Add(1)
		go func(channelID string, throughSeq uint64) {
			defer group.Done()
			result, recordErr := ledger.RecordPermanentMessageErasure(context.Background(), backupinfra.PermanentMessageErasure{
				ChannelID: channelID, ChannelType: 2, ThroughSeq: throughSeq, RequestedAtUnixMillis: 1_753_056_361_000,
			})
			results <- result
			failures <- recordErr
		}(channelID, uint64(index+20))
	}
	group.Wait()
	close(results)
	close(failures)
	for recordErr := range failures {
		require.NoError(t, recordErr)
	}
	sequences := make([]uint64, 0, len(channels))
	for result := range results {
		sequences = append(sequences, result.Sequence)
	}
	require.ElementsMatch(t, []uint64{3, 4}, sequences)
	otherSlotChannel := channelInHashSlot(t, (receipt.HashSlot+1)%256, 256, "independent-slot")
	otherSlotReceipt, err := ledger.RecordPermanentMessageErasure(context.Background(), backupinfra.PermanentMessageErasure{
		ChannelID: otherSlotChannel, ChannelType: 2, ThroughSeq: 7, RequestedAtUnixMillis: 1_753_056_362_000,
	})
	require.NoError(t, err)
	require.NotEqual(t, receipt.HashSlot, otherSlotReceipt.HashSlot)
	require.Equal(t, uint64(1), otherSlotReceipt.Sequence)
	finalSnapshot, err := loader.LoadDualSnapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(5), finalSnapshot.EventCount)
	require.Len(t, finalSnapshot.Heads, 2)
	require.Less(t, finalSnapshot.Heads[0].HashSlot, finalSnapshot.Heads[1].HashSlot)
	for _, head := range finalSnapshot.Heads {
		if head.HashSlot == receipt.HashSlot {
			require.Equal(t, uint64(4), head.Sequence)
		} else {
			require.Equal(t, uint64(1), head.Sequence)
		}
	}
	countingKeys := &countingErasureKeyManager{
		delegate: testWrappingKeyManager{mask: 0x5a},
	}
	proofCodec := backupartifact.NewObjectCodec(
		countingKeys, bytes.NewReader(bytes.Repeat([]byte{0x71}, 128)),
	)
	proofLoader, err := backupinfra.NewErasureLedgerLoader(
		backupinfra.ErasureLedgerLoaderOptions{
			Primary: primary, Secondary: secondary,
			Signer: signer, Codec: proofCodec,
			RepositoryID: "repo-prod", SourceClusterID: "cluster-a",
			SourceGeneration: "generation-1", HashSlotCount: 256,
		},
	)
	require.NoError(t, err)
	proofSnapshot, err := proofLoader.LoadDualSnapshotProof(
		context.Background(), finalSnapshot.Heads,
	)
	require.NoError(t, err)
	require.Equal(t, finalSnapshot.SHA256, proofSnapshot.SHA256)
	require.Zero(
		t, countingKeys.unwraps.Load(),
		"restore admission must authenticate ciphertext without KMS",
	)
	isolatedPrimary := &selectiveErasureReadRepository{
		Repository: primary,
		lister:     primary,
		denied: fmt.Sprintf(
			"/%04x/commits/", receipt.HashSlot,
		),
	}
	isolatedLoader, err := backupinfra.NewErasureLedgerLoader(
		backupinfra.ErasureLedgerLoaderOptions{
			Primary: isolatedPrimary, Secondary: secondary,
			Signer: signer, Codec: proofCodec,
			RepositoryID: "repo-prod", SourceClusterID: "cluster-a",
			SourceGeneration: "generation-1", HashSlotCount: 256,
		},
	)
	require.NoError(t, err)
	var isolated []backupinfra.PermanentErasureBoundary
	err = isolatedLoader.ReplayPinnedSlot(
		context.Background(), "primary", finalSnapshot.Version,
		finalSnapshot.EventCount, finalSnapshot.SHA256, finalSnapshot.Heads,
		otherSlotReceipt.HashSlot,
		func(boundary backupinfra.PermanentErasureBoundary) error {
			isolated = append(isolated, boundary)
			return nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, []backupinfra.PermanentErasureBoundary{{
		ChannelID: otherSlotChannel, ChannelType: 2, ThroughSeq: 7,
	}}, isolated)
	require.Equal(
		t, uint64(1), countingKeys.unwraps.Load(),
		"the selected Slot Leader unwraps only its own erasure stream",
	)
}

type countingErasureKeyManager struct {
	delegate testWrappingKeyManager
	unwraps  atomic.Uint64
}

func (m *countingErasureKeyManager) GenerateDataKey(
	ctx context.Context,
	keyID string,
) (backupartifact.DataKey, error) {
	return m.delegate.GenerateDataKey(ctx, keyID)
}

func (m *countingErasureKeyManager) UnwrapDataKey(
	ctx context.Context,
	keyID string,
	wrapped []byte,
) ([]byte, error) {
	m.unwraps.Add(1)
	return m.delegate.UnwrapDataKey(ctx, keyID, wrapped)
}

func TestPermanentErasureLedgerConcurrentDuplicateIsIdempotent(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	seed := sha256.Sum256([]byte("erasure-ledger-concurrent-duplicate"))
	signer := testEd25519Signer{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	store := &erasureLedgerStateStore{}
	coordinator, err := backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: 1, Store: store,
		Now: func() time.Time { return time.UnixMilli(1_753_056_360_000).UTC() },
	})
	require.NoError(t, err)
	codec := backupartifact.NewObjectCodec(testWrappingKeyManager{mask: 0x5a}, bytes.NewReader(bytes.Repeat([]byte{0x35}, 256)))
	var attempt uint64
	ledger, err := backupinfra.NewPermanentErasureLedger(backupinfra.PermanentErasureLedgerOptions{
		Primary: primary, Secondary: secondary, Codec: codec, Coordinator: coordinator,
		Signer: signer, SigningKeyID: "signing-key", KMSKeyID: "kms-key",
		RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: "generation-1", HashSlotCount: 1,
		Now: func() time.Time { return time.UnixMilli(1_753_056_360_000).UTC() },
		NewAttemptID: func() string {
			return fmt.Sprintf("attempt-%d", atomic.AddUint64(&attempt, 1))
		},
	})
	require.NoError(t, err)
	request := backupinfra.PermanentMessageErasure{
		ChannelID: "channel-duplicate", ChannelType: 2, ThroughSeq: 9, RequestedAtUnixMillis: 1_753_056_360_000,
	}

	start := make(chan struct{})
	results := make(chan backupinfra.ErasureLedgerReceipt, 2)
	failures := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			receipt, recordErr := ledger.RecordPermanentMessageErasure(context.Background(), request)
			results <- receipt
			failures <- recordErr
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(failures)

	var expected backupinfra.ErasureLedgerReceipt
	for recordErr := range failures {
		require.NoError(t, recordErr)
	}
	for receipt := range results {
		if expected.EventID == "" {
			expected = receipt
		}
		require.Equal(t, expected, receipt)
	}
	require.Equal(t, uint64(1), expected.Sequence)
	_, err = primary.Stat(context.Background(), backupartifact.ErasureLedgerCommitKey(erasureLedgerTestNamespace(), expected.HashSlot, 2))
	require.ErrorIs(t, err, backupartifact.ErrObjectNotFound)
}

func TestPermanentErasureLedgerIsolatesSourceGenerationsInSharedRepositories(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	seed := sha256.Sum256([]byte("erasure-ledger-generation-isolation"))
	signer := testEd25519Signer{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	codec := backupartifact.NewObjectCodec(testWrappingKeyManager{mask: 0x5a}, bytes.NewReader(bytes.Repeat([]byte{0x53}, 256)))

	keys := make([]string, 0, 2)
	for index, generation := range []string{"generation-1", "generation-2"} {
		coordinator, err := backupusecase.NewApp(backupusecase.Options{
			Enabled: true, HashSlotCount: 1,
			Store: &erasureLedgerStateStore{},
			Now: func() time.Time {
				return time.UnixMilli(1_753_056_360_000).UTC()
			},
		})
		require.NoError(t, err)
		ledger, err := backupinfra.NewPermanentErasureLedger(backupinfra.PermanentErasureLedgerOptions{
			Primary: primary, Secondary: secondary, Codec: codec, Coordinator: coordinator,
			Signer: signer, SigningKeyID: "signing-key", KMSKeyID: "kms-key",
			RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: generation, HashSlotCount: 1,
			Now: func() time.Time { return time.UnixMilli(1_753_056_360_000).UTC() },
			NewAttemptID: func() string {
				return fmt.Sprintf("generation-attempt-%d", index+1)
			},
		})
		require.NoError(t, err)
		receipt, err := ledger.RecordPermanentMessageErasure(context.Background(), backupinfra.PermanentMessageErasure{
			ChannelID: "shared-channel", ChannelType: 2, ThroughSeq: uint64(index + 1), RequestedAtUnixMillis: 1_753_056_360_000,
		})
		require.NoError(t, err)
		require.Equal(t, uint64(1), receipt.Sequence)
		namespace := backupartifact.ComputeErasureLedgerStreamNamespace("repo-prod", "cluster-a", generation)
		key := backupartifact.ErasureLedgerCommitKey(namespace, receipt.HashSlot, receipt.Sequence)
		keys = append(keys, key)
		_, err = primary.Stat(context.Background(), key)
		require.NoError(t, err)

		loader, err := backupinfra.NewErasureLedgerLoader(backupinfra.ErasureLedgerLoaderOptions{
			Primary: primary, Secondary: secondary, Signer: signer, Codec: codec,
			RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: generation, HashSlotCount: 1,
		})
		require.NoError(t, err)
		snapshot, err := loader.LoadDualSnapshot(context.Background())
		require.NoError(t, err)
		require.Equal(t, uint64(1), snapshot.EventCount)
		require.Len(t, snapshot.Heads, 1)
		require.Equal(t, key, snapshot.Heads[0].CommitKey)
	}
	require.NotEqual(t, keys[0], keys[1])
}

func TestPermanentErasureLedgerFailsClosedAndRepairsSecondaryCommit(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondaryFile, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	secondary := &erasureCommitFailRepository{Repository: secondaryFile, fail: true}
	seed := sha256.Sum256([]byte("erasure-ledger-failure-test"))
	signer := testEd25519Signer{privateKey: ed25519.NewKeyFromSeed(seed[:])}
	store := &erasureLedgerStateStore{}
	coordinator, err := backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: 1, Store: store,
		Now: func() time.Time { return time.UnixMilli(1_753_056_360_000).UTC() },
	})
	require.NoError(t, err)
	codec := backupartifact.NewObjectCodec(testWrappingKeyManager{mask: 0x5a}, bytes.NewReader(bytes.Repeat([]byte{0x66}, 256)))
	ledger, err := backupinfra.NewPermanentErasureLedger(backupinfra.PermanentErasureLedgerOptions{
		Primary: primary, Secondary: secondary, Codec: codec, Coordinator: coordinator,
		Signer: signer, SigningKeyID: "signing-key", KMSKeyID: "kms-key",
		RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: "generation-1", HashSlotCount: 1,
		Now: func() time.Time { return time.UnixMilli(1_753_056_360_000).UTC() }, NewAttemptID: func() string { return "failure-attempt" },
	})
	require.NoError(t, err)
	request := backupinfra.PermanentMessageErasure{
		ChannelID: "channel-failure", ChannelType: 2, ThroughSeq: 9, RequestedAtUnixMillis: 1_753_056_360_000,
	}
	_, err = ledger.RecordPermanentMessageErasure(context.Background(), request)
	require.Error(t, err)
	state, err := coordinator.CoordinationState(context.Background())
	require.NoError(t, err)
	require.Len(t, state.ErasureStreams, 1)
	require.Nil(t, state.ErasureStreams[0].Head)
	require.NotNil(t, state.ErasureStreams[0].Pending)

	secondary.setFail(false)
	receipt, err := ledger.RecordPermanentMessageErasure(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, uint64(1), receipt.Sequence)
	state, err = coordinator.CoordinationState(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), state.ErasureStreams[0].Head.Sequence)
	require.Nil(t, state.ErasureStreams[0].Pending)
}

func erasureLedgerTestNamespace() string {
	return backupartifact.ComputeErasureLedgerStreamNamespace("repo-prod", "cluster-a", "generation-1")
}

func channelInHashSlot(t *testing.T, hashSlot, hashSlotCount uint16, prefix string) string {
	t.Helper()
	for index := 0; index < 100_000; index++ {
		channelID := fmt.Sprintf("%s-%d", prefix, index)
		if routing.HashSlotForKey(channelID, hashSlotCount) == hashSlot {
			return channelID
		}
	}
	t.Fatalf("no Channel candidate found for Hash Slot %d", hashSlot)
	return ""
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

type erasureCommitFailRepository struct {
	backupartifact.Repository
	mu   sync.Mutex
	fail bool
}

type selectiveErasureReadRepository struct {
	backupartifact.Repository
	lister backupinfra.ErasureLedgerCommitLister
	denied string
}

func (r *selectiveErasureReadRepository) ListErasureLedgerCommitKeys(
	ctx context.Context,
	namespace string,
) ([]string, error) {
	return r.lister.ListErasureLedgerCommitKeys(ctx, namespace)
}

func (r *selectiveErasureReadRepository) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, backupartifact.RepositoryObject, error) {
	if strings.Contains(key, r.denied) {
		return nil, backupartifact.RepositoryObject{},
			backupartifact.ErrObjectNotFound
	}
	return r.Repository.Open(ctx, key)
}

func (r *selectiveErasureReadRepository) Stat(
	ctx context.Context,
	key string,
) (backupartifact.RepositoryObject, error) {
	if strings.Contains(key, r.denied) {
		return backupartifact.RepositoryObject{},
			backupartifact.ErrObjectNotFound
	}
	return r.Repository.Stat(ctx, key)
}

func (r *erasureCommitFailRepository) PutImmutable(ctx context.Context, key string, size int64, checksum string, body io.Reader) error {
	r.mu.Lock()
	fail := r.fail
	r.mu.Unlock()
	if fail && strings.Contains(key, "/commits/") {
		return io.ErrUnexpectedEOF
	}
	return r.Repository.PutImmutable(ctx, key, size, checksum, body)
}

func (r *erasureCommitFailRepository) setFail(fail bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail = fail
}
