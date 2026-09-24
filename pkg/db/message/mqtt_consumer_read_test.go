package message

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/stretchr/testify/require"
)

func TestMQTTConsumerPagesPreserveAnchoredContentAfterTrimAndRestart(t *testing.T) {
	f := repairPlanFixture(t)
	ctx := context.Background()
	expected := f.page(t, 1, 4)
	_, err := f.source.ReleaseMQTTSourceAtAnchor(ctx, f.generation, 5)
	require.NoError(t, err)
	trimmed, err := f.source.log.TrimPrefixThrough(ctx, 4)
	require.NoError(t, err)
	require.Equal(t, 4, trimmed.Deleted)
	before, _, err := f.source.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	// Read a short page even though the selected anchor covers a larger prefix.
	first, err := f.source.ReadMQTTReplayAnchor(ctx, f.generation, 5, 1, 4, ReadOptions{Limit: 2, MaxBytes: 16 << 20})
	require.NoError(t, err)
	require.Equal(t, expected.Records[:2], first.Records)
	require.EqualValues(t, 2, first.After.Through)
	second, err := f.source.ReadMQTTReplayAnchor(ctx, f.generation, 5, 3, 4, ReadOptions{Limit: 256, MaxBytes: len(expected.Records[2].Content)})
	require.NoError(t, err)
	require.Equal(t, expected.Records[2:3], second.Records)
	require.EqualValues(t, 2, second.Before.Through)
	last, err := f.source.ReadMQTTReplayAnchor(ctx, f.generation, 5, 4, 4, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, expected.Records[3:], last.Records)
	require.Equal(t, expected.After, last.After)
	require.Equal(t, first.After, second.Before)
	require.Equal(t, second.After, last.Before)
	after, _, err := f.source.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "consumer reads cannot advance source release")
	// Native entry identities and shared content survive without original rows.
	full, err := f.source.ExportMQTTReplayAnchor(ctx, 5, 1, replayTransferBudget)
	require.NoError(t, err)
	_, err = f.target.ImportMQTTReplayAnchor(ctx, 5, full)
	require.NoError(t, err)
	require.NoError(t, f.target.Close())
	require.NoError(t, f.targetEngine.Close())
	f.targetEngine, err = Open(f.targetPath)
	require.NoError(t, err)
	f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	reopened, err := f.target.ReadMQTTReplayAnchor(ctx, f.generation, 5, 2, 3, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, expected.Records[1:3], reopened.Records)
	clear(reopened.Records[0].Content)
	again, err := f.target.ReadMQTTReplayAnchor(ctx, f.generation, 5, 2, 3, replayTransferBudget)
	require.NoError(t, err)
	require.Equal(t, expected.Records[1:3], again.Records)
}

func TestMQTTConsumerPagesRejectUnprovenOrCorruptCoverage(t *testing.T) {
	for _, fault := range []string{"absent_anchor", "pending_anchor", "wrong_generation", "beyond_anchor", "missing_content", "partial_content", "missing_endpoint", "wrong_endpoint", "missing_row", "corrupt_row", "too_small", "canceled"} {
		t.Run(fault, func(t *testing.T) {
			f := repairPlanFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := f.source
			generation, anchor, through := f.generation, uint64(5), uint64(4)
			opts := ReadOptions{Limit: 1, MaxBytes: 16 << 20}
			switch fault {
			case "absent_anchor":
				anchor = 4
			case "pending_anchor":
				setPhysicalTestValue(t, f.sourceEngine, encodeCheckpointKey(f.source.log.key), encodeCheckpoint(Checkpoint{HW: 4}))
			case "wrong_generation":
				generation = "foreign"
			case "beyond_anchor":
				through = 6
			case "missing_content":
				store = f.target
			case "partial_content":
				store = f.target
				_, e := store.log.CopyMQTTReplaySource(ctx, f.generation, 1, 1, replayTransferBudget)
				require.NoError(t, e)
			case "missing_endpoint":
				deletePhysicalTestKey(t, f.sourceEngine, mqttReplayMeterKey(store.log.key, generation, 4))
			case "wrong_endpoint":
				key := mqttReplayMeterKey(store.log.key, generation, 4)
				v, found, e := f.sourceEngine.engine.Get(key)
				require.NoError(t, e)
				require.True(t, found)
				env, e := rowcodec.UnwrapBorrowed(key, v)
				require.NoError(t, e)
				env.Payload[16] ^= 1
				setPhysicalTestValue(t, f.sourceEngine, key, rowcodec.Wrap(key, env.Version, env.Codec, env.Flags, env.Payload))
			case "missing_row":
				deletePhysicalTestKey(t, f.sourceEngine, mqttReplayRowKey(store.log.key, generation, 1))
			case "corrupt_row":
				setPhysicalTestValue(t, f.sourceEngine, mqttReplayRowKey(store.log.key, generation, 1), []byte("corrupt"))
			case "too_small":
				opts.MaxBytes = 1
			case "canceled":
				cancel()
			}
			got, e := store.ReadMQTTReplayAnchor(ctx, generation, anchor, 1, through, opts)
			require.Error(t, e)
			require.Zero(t, got)
		})
	}
}
