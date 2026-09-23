package message

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/stretchr/testify/require"
)

func TestMQTTReplayAnchorRepairAfterOriginalTrimAndRestart(t *testing.T) {
	f := newReplayTransferFixture(t)
	ctx := context.Background()
	m, records, _ := replayAnchorProposal(t, f, 3)
	for _, s := range []*ChannelStore{f.source, f.target} {
		appendMQTTActivation(t, s, m, records, 5)
		source, _, err := s.log.LoadMQTTSourceState(ctx)
		require.NoError(t, err)
		// Test-only release authority models originals already reclaimed. Repair
		// must authenticate against the receiver's journal, not this digest.
		released := source
		released.Revision, released.CopiedThrough, released.ReceiptDigest = 2, 4, [32]byte{9}
		require.NoError(t, s.log.ApplyMQTTSourceState(ctx, source.Revision, released))
		trim, err := s.log.TrimPrefixThrough(ctx, 4)
		require.NoError(t, err)
		require.Equal(t, 4, trim.Deleted)
	}
	source, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	checkpoint, err := f.target.LoadCheckpoint()
	require.NoError(t, err)
	page, err := f.source.ExportMQTTReplayAnchor(ctx, 5, 1, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, f.all, page)
	for i := 0; i < 2; i++ {
		got, err := f.target.ImportMQTTReplayAnchor(ctx, 5, page)
		require.NoError(t, err)
		require.Equal(t, page.After, got)
		require.NoError(t, f.target.Close())
		require.NoError(t, f.targetEngine.Close())
		f.targetEngine, err = Open(f.targetPath)
		require.NoError(t, err)
		f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	}
	repaired, err := f.target.ExportMQTTReplayAnchor(ctx, 5, 1, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, page, repaired)
	after, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	require.Equal(t, source, after)
	cp, err := f.target.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, checkpoint, cp)
	for pos := uint64(1); pos <= 4; pos++ {
		_, present, err := f.targetEngine.engine.Get(encodeMessageRowKey(f.target.log.key, pos, 0))
		require.NoError(t, err)
		require.False(t, present, "repair must not resurrect ordinary history")
	}
}

func TestMQTTReplayAnchorRepairRequiresIndependentCommittedProof(t *testing.T) {
	for _, fault := range []string{"absent", "pending", "journal", "entry", "checkpoint", "activation", "native_forgery", "bad_last", "short_page", "gap"} {
		t.Run(fault, func(t *testing.T) {
			f := newReplayTransferFixture(t)
			ctx := context.Background()
			m, records, _ := replayAnchorProposal(t, f, 3)
			if fault != "absent" {
				hw := uint64(5)
				if fault == "pending" {
					hw = 4
				}
				appendMQTTActivation(t, f.target, m, records, hw)
			}
			page := f.page(t, 1, 4)
			key := f.target.log.key
			switch fault {
			case "journal":
				deletePhysicalTestKey(t, f.targetEngine, mqttReplayAnchorKey(key, 5))
			case "entry":
				deletePhysicalTestKey(t, f.targetEngine, encodeEntryIdentityKey(key, 5))
			case "checkpoint":
				deletePhysicalTestKey(t, f.targetEngine, encodeCheckpointKey(key))
			case "activation":
				deletePhysicalTestKey(t, f.targetEngine, mqttActivationKey(key))
			case "native_forgery":
				r := &page.Records[3]
				row, err := mqttReplayOriginalRow(key, r.Position, r.Content)
				require.NoError(t, err)
				row.FramerFlags ^= 2 // Not bound by the native log identity.
				r.Content, err = encodeMessageHeader(encodeMessageRowKey(key, r.Position, 0), row)
				require.NoError(t, err)
				resealReplayTransfer(t, key, &page)
			case "bad_last":
				page.Records[3].ContentHash[0] ^= 1
			case "short_page":
				page = f.page(t, 1, 3)
			case "gap":
				page = f.page(t, 2, 4)
			}
			_, err := f.target.log.ImportMQTTReplayAnchor(ctx, 5, page)
			require.Error(t, err)
			f.requireEmpty(t)
		})
	}
}

func TestMQTTReplayAnchorExportMustReachExactAnchor(t *testing.T) {
	f := newReplayTransferFixture(t)
	ctx := context.Background()
	m, records, _ := replayAnchorProposal(t, f, 3)
	appendMQTTActivation(t, f.source, m, records, 5)
	for _, opts := range []ReadOptions{{Limit: 3, MaxBytes: 16 << 20}, {Limit: 256, MaxBytes: len(f.all.Records[0].Content)}, {Limit: 257, MaxBytes: 16 << 20}, {Limit: 256, MaxBytes: 16<<20 + 1}} {
		page, err := f.source.log.ExportMQTTReplayAnchor(ctx, 5, 1, opts)
		require.Error(t, err)
		require.Empty(t, page.Records, "a short page has no independently accepted endpoint")
	}
	for _, pos := range []uint64{0, 1, 4, 6} {
		_, err := f.source.log.ExportMQTTReplayAnchor(ctx, pos, 1, replayTransferBudget)
		require.Error(t, err)
	}
	for _, from := range []uint64{0, 5, ^uint64(0)} {
		_, err := f.source.log.ExportMQTTReplayAnchor(ctx, 5, from, replayTransferBudget)
		require.Error(t, err)
	}
	p, err := f.source.log.ExportMQTTReplayAnchor(ctx, 5, 3, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, f.page(t, 3, 4), p)
	clear(p.Records[0].Content)
	got, err := f.source.log.ExportMQTTReplayAnchor(ctx, 5, 3, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, f.page(t, 3, 4), got)
}

func TestMQTTReplayAnchorRepairPreservesLocalPrefixAndCancellation(t *testing.T) {
	f := newReplayTransferFixture(t)
	ctx := context.Background()
	m, records, _ := replayAnchorProposal(t, f, 3)
	for _, s := range []*ChannelStore{f.source, f.target} {
		appendMQTTActivation(t, s, m, records, 5)
	}
	_, err := f.target.log.CopyMQTTReplaySource(ctx, f.generation, 1, 2, replayTransferBudget)
	require.NoError(t, err)
	initial, _, err := f.target.log.LoadMQTTReplayState(ctx)
	require.NoError(t, err)
	_, err = f.target.log.ImportMQTTReplayAnchor(ctx, 5, f.all)
	require.ErrorIs(t, err, dberrors.ErrConflict)
	state, _, err := f.target.log.LoadMQTTReplayState(ctx)
	require.NoError(t, err)
	require.Equal(t, initial, state)
	remaining := f.page(t, 3, 4)
	got, err := f.target.log.ImportMQTTReplayAnchor(ctx, 5, remaining)
	require.NoError(t, err)
	require.Equal(t, f.all.After, got)
	_, err = f.target.log.CopyMQTTReplaySource(ctx, f.generation, 5, 5, replayTransferBudget)
	require.NoError(t, err)
	got, err = f.target.log.ImportMQTTReplayAnchor(ctx, 5, f.all)
	require.NoError(t, err)
	require.Equal(t, f.all.After, got, "historical retry retains its exact result")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = f.target.log.ImportMQTTReplayAnchor(canceled, 5, f.all)
	require.ErrorIs(t, err, context.Canceled)
	_, err = f.source.log.ExportMQTTReplayAnchor(canceled, 5, 1, replayTransferBudget)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, f.target.Close())
	_, err = f.target.ImportMQTTReplayAnchor(ctx, 5, f.all)
	require.Error(t, err)
	_, err = f.target.ExportMQTTReplayAnchor(ctx, 5, 1, replayTransferBudget)
	require.Error(t, err)
}
