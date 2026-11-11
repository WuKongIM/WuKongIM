package message

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/stretchr/testify/require"
)

// Replay canonicalizes PayloadSize while native funding retains an accounting
// hint. Cover sufficient prepaid debt with/without originals, exact retry,
// underfunding, malformed receipts, forged content, cancellation and missing debt
// at a full escrow. No failed import may publish rows or change the receipt.
func TestMQTTReplayAnchorImportRetainsSufficientPrepaidStorage(t *testing.T) {
	for _, mode := range []string{"native-hint", "trimmed", "underfunded", "corrupt-receipt", "forged-content", "canceled", "missing-charge", "original-booking"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newReplayTransferFixtureWithTargetHint(t, 256)
			m, records, _ := replayAnchorProposal(t, f, 3)
			appendMQTTActivation(t, f.target, m, records, 5)
			f.targetEngine.ConfigureMQTTStorage(MQTTStorageOptions{
				NodeBytes: 1, ClusterBytes: 1,
				Members: func() ([]uint64, uint64) { return []uint64{1}, 1 },
				Adjust: func(context.Context, MQTTStorageGrantRequest) (MQTTStorageGrantReply, error) {
					t.Fatal("already funded replay must not acquire a new grant")
					return MQTTStorageGrantReply{}, nil
				},
			})
			budget := f.targetEngine.db.mqttStorage
			require.NoError(t, budget.readyErr)
			used := budget.used
			require.Greater(t, used, uint64(1), "fixture begins above its lowered limit")
			key := mqttStorageChargeKey(f.target.log.key, 2)
			receipt, found, err := f.targetEngine.engine.Get(key)
			require.NoError(t, err)
			require.True(t, found)
			old, err := decodeMQTTStorageCharge(key, receipt)
			require.NoError(t, err)
			canonical, err := mqttReplayOriginalRow(f.target.log.key, 2, f.all.Records[1].Content)
			require.NoError(t, err)
			need := mqttStorageRowBytes(f.target.log.key, f.generation, canonical)
			require.Greater(t, old, need, "native size hint must cross a varint width")
			if mode == "original-booking" {
				_, _, err := f.target.log.prepareMQTTStorageBooking(ctx, []messageRow{canonical}, []durableProposalRecord{{manifest: f.business}})
				require.ErrorIs(t, err, dberrors.ErrCorruptState, "ordinary funding still requires its exact charge")
				require.Equal(t, used, budget.used)
				f.requireEmpty(t)
				return
			}
			page := f.all
			switch mode {
			case "trimmed":
				source, _, err := f.target.log.LoadMQTTSourceState(ctx)
				require.NoError(t, err)
				released := source
				released.Revision++
				released.CopiedThrough = 4
				released.ReceiptDigest = f.all.After.Digest
				require.NoError(t, f.target.log.ApplyMQTTSourceState(ctx, source.Revision, released))
				trim, err := f.target.log.TrimPrefixThrough(ctx, 4)
				require.NoError(t, err)
				require.Equal(t, 4, trim.Deleted)
			case "underfunded":
				batch := f.targetEngine.engine.NewBatch()
				require.NoError(t, stageMQTTStorageCharges(batch, f.target.log.key, []mqttStorageCharge{{2, need - 1}}))
				require.NoError(t, batch.Commit(true))
				require.NoError(t, batch.Close())
			case "corrupt-receipt":
				batch := f.targetEngine.engine.NewBatch()
				require.NoError(t, batch.Set(key, []byte("bad")))
				require.NoError(t, batch.Commit(true))
				require.NoError(t, batch.Close())
			case "forged-content":
				page.Records = append([]MQTTReplayRecord(nil), page.Records...)
				page.Records[1].Content = append([]byte(nil), page.Records[1].Content...)
				page.Records[1].Content[len(page.Records[1].Content)-1] ^= 1
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "missing-charge":
				deletePhysicalTestKey(t, f.targetEngine, key)
			}
			before, beforeFound, err := f.targetEngine.engine.Get(key)
			require.NoError(t, err)
			got, err := f.target.log.ImportMQTTReplayAnchor(ctx, 5, page)
			if mode == "native-hint" || mode == "trimmed" {
				require.NoError(t, err)
				require.Equal(t, page.After, got)
				got, err = f.target.log.ImportMQTTReplayAnchor(ctx, 5, page)
				require.NoError(t, err)
				require.Equal(t, page.After, got)
			} else {
				require.Error(t, err)
				if mode == "underfunded" {
					require.ErrorIs(t, err, dberrors.ErrCorruptState)
				}
				if mode == "canceled" {
					require.ErrorIs(t, err, context.Canceled)
				}
				f.requireEmpty(t)
			}
			after, afterFound, err := f.targetEngine.engine.Get(key)
			require.NoError(t, err)
			require.Equal(t, beforeFound, afterFound)
			require.Equal(t, before, after)
			require.Equal(t, used, budget.used, "import retains existing responsibility without refund or recharge")
		})
	}
}
