package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestMessageCursorResolverRebuildsLatestBoundariesFromCursorSidecarChain(t *testing.T) {
	firstReference := testContinuousSegmentReference("a")
	secondReference := testContinuousSegmentReference("b")
	firstBody, err := backupartifact.MarshalMessageCursorBatch(backupartifact.MessageCursorBatch{
		HashSlot:   17,
		Generation: "slot-generation-1", Sequence: 1,
		FromCursor: "", NextCursor: "channels/a", SourceHighWatermark: 3,
		WatermarkAtUnixMillis: 1_753_400_090_000,
		Boundaries: []backupartifact.ChannelBoundary{
			{ChannelID: "channel-a", ChannelType: 2, Epoch: 1, HW: 3},
			{ChannelID: "channel-b", ChannelType: 2, Epoch: 1, HW: 1},
		},
	})
	require.NoError(t, err)
	secondBody, err := backupartifact.MarshalMessageCursorBatch(backupartifact.MessageCursorBatch{
		HashSlot:   17,
		Generation: "slot-generation-1", Sequence: 2, Previous: &firstReference,
		FromCursor: "channels/a", NextCursor: "channels/z", SourceHighWatermark: 5,
		WatermarkAtUnixMillis: 1_753_400_100_000,
		Boundaries: []backupartifact.ChannelBoundary{
			{ChannelID: "channel-a", ChannelType: 2, Epoch: 1, HW: 5},
		},
	})
	require.NoError(t, err)
	loader := &recordingSegmentLoader{bodies: map[string][]byte{
		firstReference.SegmentID: firstBody, secondReference.SegmentID: secondBody,
	}}
	resolver, err := backupinfra.NewMessageCursorResolver(loader)
	require.NoError(t, err)

	resolved, err := resolver.Resolve(context.Background(), backupinfra.MessageCursorResolveRequest{
		Head: secondReference, HashSlot: 17, Generation: "slot-generation-1",
		Sequence: 2, SourceCursor: "channels/z", SourceHighWatermark: 5,
	})
	require.NoError(t, err)
	require.Equal(t, []backupartifact.ChannelBoundary{
		{ChannelID: "channel-a", ChannelType: 2, Epoch: 1, HW: 5},
		{ChannelID: "channel-b", ChannelType: 2, Epoch: 1, HW: 1},
	}, resolved)
	require.Equal(t, []string{secondReference.SegmentID, firstReference.SegmentID}, loader.loads)
	_, err = resolver.Resolve(context.Background(), backupinfra.MessageCursorResolveRequest{
		Head: secondReference, HashSlot: 17, Generation: "slot-generation-1",
		Sequence: 2, SourceCursor: "channels/z", SourceHighWatermark: 5,
	})
	require.NoError(t, err)
	require.Equal(t, []string{secondReference.SegmentID, firstReference.SegmentID}, loader.loads)
}

func TestMessageCursorResolverLoadsOnlyNewSidecarAfterCachedTip(t *testing.T) {
	firstReference := testContinuousSegmentReference("a")
	secondReference := testContinuousSegmentReference("b")
	thirdReference := testContinuousSegmentReference("d")
	firstBody, err := backupartifact.MarshalMessageCursorBatch(backupartifact.MessageCursorBatch{
		HashSlot: 17, Generation: "slot-generation-1", Sequence: 1,
		NextCursor: "channels/a", SourceHighWatermark: 1,
		WatermarkAtUnixMillis: 1_753_400_090_000,
		Boundaries: []backupartifact.ChannelBoundary{
			{ChannelID: "channel-a", ChannelType: 2, Epoch: 1, HW: 1},
		},
	})
	require.NoError(t, err)
	secondBody, err := backupartifact.MarshalMessageCursorBatch(backupartifact.MessageCursorBatch{
		HashSlot: 17, Generation: "slot-generation-1", Sequence: 2, Previous: &firstReference,
		FromCursor: "channels/a", NextCursor: "channels/b", SourceHighWatermark: 2,
		WatermarkAtUnixMillis: 1_753_400_090_100,
		Boundaries: []backupartifact.ChannelBoundary{
			{ChannelID: "channel-b", ChannelType: 2, Epoch: 1, HW: 1},
		},
	})
	require.NoError(t, err)
	thirdBody, err := backupartifact.MarshalMessageCursorBatch(backupartifact.MessageCursorBatch{
		HashSlot: 17, Generation: "slot-generation-1", Sequence: 3, Previous: &secondReference,
		FromCursor: "channels/b", NextCursor: "channels/c", SourceHighWatermark: 3,
		WatermarkAtUnixMillis: 1_753_400_090_200,
		Boundaries: []backupartifact.ChannelBoundary{
			{ChannelID: "channel-c", ChannelType: 2, Epoch: 1, HW: 1},
		},
	})
	require.NoError(t, err)
	loader := &recordingSegmentLoader{bodies: map[string][]byte{
		firstReference.SegmentID:  firstBody,
		secondReference.SegmentID: secondBody,
		thirdReference.SegmentID:  thirdBody,
	}}
	resolver, err := backupinfra.NewMessageCursorResolver(loader)
	require.NoError(t, err)

	_, err = resolver.Resolve(context.Background(), backupinfra.MessageCursorResolveRequest{
		Head: secondReference, HashSlot: 17, Generation: "slot-generation-1",
		Sequence: 2, SourceCursor: "channels/b", SourceHighWatermark: 2,
	})
	require.NoError(t, err)
	resolved, err := resolver.Resolve(context.Background(), backupinfra.MessageCursorResolveRequest{
		Head: thirdReference, HashSlot: 17, Generation: "slot-generation-1",
		Sequence: 3, SourceCursor: "channels/c", SourceHighWatermark: 3,
	})
	require.NoError(t, err)
	require.Equal(t, []backupartifact.ChannelBoundary{
		{ChannelID: "channel-a", ChannelType: 2, Epoch: 1, HW: 1},
		{ChannelID: "channel-b", ChannelType: 2, Epoch: 1, HW: 1},
		{ChannelID: "channel-c", ChannelType: 2, Epoch: 1, HW: 1},
	}, resolved)
	require.Equal(t, []string{
		secondReference.SegmentID, firstReference.SegmentID, thirdReference.SegmentID,
	}, loader.loads)
}

func TestMessageCursorResolverRejectsBrokenSequenceAndCursorChain(t *testing.T) {
	firstReference := testContinuousSegmentReference("a")
	secondReference := testContinuousSegmentReference("b")
	body, err := backupartifact.MarshalMessageCursorBatch(backupartifact.MessageCursorBatch{
		HashSlot:   17,
		Generation: "slot-generation-1", Sequence: 2, Previous: &firstReference,
		FromCursor: "wrong-cursor", NextCursor: "channels/z", SourceHighWatermark: 5,
		WatermarkAtUnixMillis: 1_753_400_100_000,
		Boundaries: []backupartifact.ChannelBoundary{
			{ChannelID: "channel-a", ChannelType: 2, Epoch: 1, HW: 5},
		},
	})
	require.NoError(t, err)
	resolver, err := backupinfra.NewMessageCursorResolver(&recordingSegmentLoader{
		bodies: map[string][]byte{secondReference.SegmentID: body},
	})
	require.NoError(t, err)

	_, err = resolver.Resolve(context.Background(), backupinfra.MessageCursorResolveRequest{
		Head: secondReference, HashSlot: 17, Generation: "slot-generation-1",
		Sequence: 2, SourceCursor: "different-tip", SourceHighWatermark: 5,
	})
	require.Error(t, err)
}

func TestMessageCursorResolverStopsAtFullCheckpoint(t *testing.T) {
	reference := testContinuousSegmentReference("e")
	body, err := backupartifact.MarshalMessageCursorBatch(backupartifact.MessageCursorBatch{
		HashSlot: 17, Generation: "slot-generation-1", Sequence: 1024,
		Checkpoint: true, NextCursor: "checkpoint-1024",
		SourceHighWatermark: 2048, WatermarkAtUnixMillis: 1_753_400_100_000,
		Boundaries: []backupartifact.ChannelBoundary{
			{ChannelID: "channel-a", ChannelType: 2, Epoch: 3, HW: 2048},
		},
	})
	require.NoError(t, err)
	loader := &recordingSegmentLoader{bodies: map[string][]byte{reference.SegmentID: body}}
	resolver, err := backupinfra.NewMessageCursorResolver(loader)
	require.NoError(t, err)

	resolved, err := resolver.Resolve(context.Background(), backupinfra.MessageCursorResolveRequest{
		Head: reference, HashSlot: 17, Generation: "slot-generation-1",
		Sequence: 1024, SourceCursor: "checkpoint-1024", SourceHighWatermark: 2048,
	})
	require.NoError(t, err)
	require.Equal(t, []backupartifact.ChannelBoundary{
		{ChannelID: "channel-a", ChannelType: 2, Epoch: 3, HW: 2048},
	}, resolved)
	require.Equal(t, []string{reference.SegmentID}, loader.loads)
}

func TestMessageCursorResolverCacheEvictsLeastRecentlyUsedSlot(t *testing.T) {
	const slots = 257
	loader := &recordingSegmentLoader{bodies: make(map[string][]byte, slots)}
	requests := make([]backupinfra.MessageCursorResolveRequest, 0, slots)
	for hashSlot := 0; hashSlot < slots; hashSlot++ {
		id := fmt.Sprintf("%064x", hashSlot+1)
		cursor := fmt.Sprintf("cursor-%d", hashSlot)
		body, err := backupartifact.MarshalMessageCursorBatch(backupartifact.MessageCursorBatch{
			HashSlot: uint16(hashSlot), Generation: "slot-generation-1", Sequence: 1,
			NextCursor: cursor, SourceHighWatermark: 1,
			WatermarkAtUnixMillis: 1_753_400_100_000,
			Boundaries: []backupartifact.ChannelBoundary{{
				ChannelID: fmt.Sprintf("channel-%d", hashSlot), ChannelType: 2, Epoch: 1, HW: 1,
			}},
		})
		require.NoError(t, err)
		reference := backupartifact.SegmentReference{
			SegmentID: id, CommitKey: "segments/" + id + "/commit.json",
			CommitSHA256: strings.Repeat("c", 64), PlaintextBytes: int64(len(body)),
		}
		loader.bodies[id] = body
		requests = append(requests, backupinfra.MessageCursorResolveRequest{
			Head: reference, HashSlot: uint16(hashSlot), Generation: "slot-generation-1",
			Sequence: 1, SourceCursor: cursor, SourceHighWatermark: 1,
		})
	}
	resolver, err := backupinfra.NewMessageCursorResolver(loader)
	require.NoError(t, err)
	for _, request := range requests {
		_, err := resolver.Resolve(context.Background(), request)
		require.NoError(t, err)
	}
	require.Len(t, loader.loads, slots)
	_, err = resolver.Resolve(context.Background(), requests[0])
	require.NoError(t, err)
	require.Len(t, loader.loads, slots+1)
}

func TestMessageCursorResolverRestartsFromReplicatedRepositoryStore(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", filepath.Join(t.TempDir(), "primary"))
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", filepath.Join(t.TempDir(), "secondary"))
	require.NoError(t, err)
	seed := sha256.Sum256([]byte("continuous-cursor-repository-test"))
	store, err := backupartifact.NewReplicatedSegmentStore(
		primary,
		secondary,
		backupartifact.NewSegmentCodec(
			testWrappingKeyManager{mask: 0xa5},
			bytes.NewReader(bytes.Repeat([]byte{0x41}, 128)),
		),
		testEd25519Signer{privateKey: ed25519.NewKeyFromSeed(seed[:])},
		"signing-key",
	)
	require.NoError(t, err)
	firstBody, err := backupartifact.MarshalMessageCursorBatch(backupartifact.MessageCursorBatch{
		HashSlot:   17,
		Generation: "slot-generation-1", Sequence: 1,
		NextCursor: "channels/a", SourceHighWatermark: 3,
		WatermarkAtUnixMillis: 1_753_400_090_000,
		Boundaries: []backupartifact.ChannelBoundary{
			{ChannelID: "channel-a", ChannelType: 2, Epoch: 1, HW: 3},
		},
	})
	require.NoError(t, err)
	firstReference, err := store.Commit(context.Background(), continuousCursorDescriptor(1), firstBody)
	require.NoError(t, err)
	secondBody, err := backupartifact.MarshalMessageCursorBatch(backupartifact.MessageCursorBatch{
		HashSlot:   17,
		Generation: "slot-generation-1", Sequence: 2, Previous: &firstReference,
		FromCursor: "channels/a", NextCursor: "channels/z", SourceHighWatermark: 5,
		WatermarkAtUnixMillis: 1_753_400_100_000,
		Boundaries: []backupartifact.ChannelBoundary{
			{ChannelID: "channel-a", ChannelType: 2, Epoch: 1, HW: 5},
			{ChannelID: "channel-b", ChannelType: 2, Epoch: 1, HW: 2},
		},
	})
	require.NoError(t, err)
	secondReference, err := store.Commit(context.Background(), continuousCursorDescriptor(2), secondBody)
	require.NoError(t, err)

	restarted, err := backupinfra.NewMessageCursorResolver(store)
	require.NoError(t, err)
	resolved, err := restarted.Resolve(context.Background(), backupinfra.MessageCursorResolveRequest{
		Head: secondReference, HashSlot: 17, Generation: "slot-generation-1",
		Sequence: 2, SourceCursor: "channels/z", SourceHighWatermark: 5,
	})
	require.NoError(t, err)
	require.Equal(t, []backupartifact.ChannelBoundary{
		{ChannelID: "channel-a", ChannelType: 2, Epoch: 1, HW: 5},
		{ChannelID: "channel-b", ChannelType: 2, Epoch: 1, HW: 2},
	}, resolved)
}

func continuousCursorDescriptor(sequence uint64) backupartifact.SegmentDescriptor {
	return backupartifact.SegmentDescriptor{
		Logical: backupartifact.SegmentLogicalDescriptor{
			RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
			SourceGeneration: "source-generation-1", Generation: "slot-generation-1",
			HashSlot: 17, Stream: backupartifact.SegmentStreamMessageCursor,
			Sequence: sequence, RecordCount: 1,
		},
		KMSKeyID: "kms-backup",
	}
}

type recordingSegmentLoader struct {
	bodies map[string][]byte
	loads  []string
}

func (l *recordingSegmentLoader) Load(_ context.Context, reference backupartifact.SegmentReference) ([]byte, error) {
	l.loads = append(l.loads, reference.SegmentID)
	body, ok := l.bodies[reference.SegmentID]
	if !ok {
		return nil, fmt.Errorf("missing segment %s", reference.SegmentID)
	}
	return append([]byte(nil), body...), nil
}

func testContinuousSegmentReference(letter string) backupartifact.SegmentReference {
	id := strings.Repeat(letter, 64)
	return backupartifact.SegmentReference{
		SegmentID: id, CommitKey: "segments/" + id + "/commit.json",
		CommitSHA256: strings.Repeat("c", 64), PlaintextBytes: 1,
	}
}
