package transfer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// Failure cases precede the guard implementation: active and ended UID bindings;
// every MQTT metadata row/index/system family; replay without a catalog/Session;
// retained source, activation, anchor, retirement, Will and funding evidence;
// state outside the requested Slot range; cancellation; and destructive overwrite.
// These bounded storage fixtures exercise the public export boundary. Raw keys
// deliberately model orphan/corrupt recovery evidence that normal writers reject.
func TestExportBundleRejectsPersistentMQTTBindingBeforeOverwrite(t *testing.T) {
	for _, state := range []metadb.MQTTSessionState{metadb.MQTTSessionActive, metadb.MQTTSessionEnded} {
		t.Run(fmt.Sprint(state), func(t *testing.T) {
			ctx := context.Background()
			store, opts := openExportNodeStore(t, t.TempDir())
			row := metadb.MQTTSession{
				Namespace: "main", ClientID: "client", UID: "alice", Generation: 1, Revision: 1,
				OwnerGeneration: 1, OwnerNodeID: 1, OwnerBootID: "boot", ConnectionID: 1,
				State: metadb.MQTTSessionActive, LeaseUntilMS: 5000, SessionExpirySec: 86400, DeviceFlag: 1,
				ReceiveMaximum: 64, MaxPacketBytes: 1 << 20, NextPacketID: 1, NextDeliveryOrder: 1,
				QuotaMessages: 10000, QuotaBytes: 64 << 20, UpdatedAtMS: 1000,
			}
			batch := store.Meta().NewBatch()
			applied, err := batch.CompareAndSwapMQTTSession(255, 0, row)
			require.NoError(t, err)
			require.NoError(t, batch.Commit(ctx))
			require.Equal(t, metadb.MQTTSessionCASApplied, applied.Status)
			require.NoError(t, batch.Close())
			if state == metadb.MQTTSessionEnded {
				row.State, row.Revision = state, 2
				row.LeaseUntilMS, row.TerminationReason = 0, metadb.MQTTSessionExplicit
				batch = store.Meta().NewBatch()
				applied, err = batch.CompareAndSwapMQTTSession(255, 1, row)
				require.NoError(t, err)
				require.NoError(t, batch.Commit(ctx))
				require.Equal(t, metadb.MQTTSessionCASApplied, applied.Status)
				require.NoError(t, batch.Close())
			}
			require.NoError(t, store.Close())
			read, err := inspect.OpenStore(inspect.Options{MetaPath: opts.MetaPath, MessagePath: opts.MessagePath, HashSlotCount: 256})
			require.NoError(t, err)
			defer read.Close()
			root := t.TempDir()
			sentinel := filepath.Join(root, "existing-bundle")
			require.NoError(t, os.WriteFile(sentinel, []byte("preserve"), 0600))
			// A smaller caller range cannot hide persisted MQTT state.
			stats, err := ExportBundle(ctx, root, read, ExportOptions{HashSlotCount: 16, Overwrite: true})
			require.ErrorIs(t, err, ErrValidation)
			require.Contains(t, err.Error(), "MQTT state")
			require.Equal(t, ExportStats{}, stats)
			got, err := os.ReadFile(sentinel)
			require.NoError(t, err)
			require.Equal(t, "preserve", string(got))
			actual, found, err := read.Meta().HashSlot(255).GetMQTTSession(ctx, "main", "client")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, row, actual)
		})
	}
}

func TestExportBundleRejectsMQTTRecoveryEvidenceWithoutSession(t *testing.T) {
	type evidence struct {
		name string
		meta bool
		key  []byte
	}
	var cases []evidence
	// The seven registered MQTT tables include retained invalidation floors and
	// capacity ledger state, even after all primary consumer rows are gone.
	for table := uint32(22); table <= 28; table++ {
		for _, space := range []keycodec.Space{keycodec.SpaceRow, keycodec.SpaceIndex, keycodec.SpaceSystem} {
			key := []byte{byte(keycodec.DomainMeta), byte(keycodec.PartitionHashSlot), 0, 255, byte(space)}
			key = keycodec.AppendUint32(key, table)
			key = append(key, 0, 1, 0)
			cases = append(cases, evidence{fmt.Sprintf("meta-table-%d-space-%d", table, space), true, key})
		}
	}
	channelPrefix := []byte{byte(keycodec.DomainMessage), byte(keycodec.PartitionChannel)}
	channelPrefix = keycodec.AppendString(channelPrefix, "orphan:2")
	for _, space := range []keycodec.Space{keycodec.SpaceRow, keycodec.SpaceIndex, keycodec.SpaceSystem} {
		key := append(append([]byte(nil), channelPrefix...), byte(space))
		key = keycodec.AppendUint32(key, 2) // shared replay table
		cases = append(cases, evidence{fmt.Sprintf("replay-space-%d", space), false, append(key, 0, 1)})
	}
	for system := uint16(12); system <= 18; system++ {
		key := append(append([]byte(nil), channelPrefix...), byte(keycodec.SpaceSystem))
		key = keycodec.AppendUint32(key, 1)
		cases = append(cases, evidence{fmt.Sprintf("message-system-%d", system), false, keycodec.AppendUint16(key, system)})
	}
	key := append(append([]byte(nil), channelPrefix...), byte(keycodec.SpaceIndex))
	key = keycodec.AppendUint32(key, 1)
	cases = append(cases, evidence{"server-will-index", false, keycodec.AppendUint16(key, 8)})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, opts := openExportNodeStore(t, t.TempDir())
			require.NoError(t, store.Close())
			path := opts.MessagePath
			if tc.meta {
				path = opts.MetaPath
			}
			eng, err := engine.Open(path, engine.Options{})
			require.NoError(t, err)
			batch := eng.NewBatch()
			require.NoError(t, batch.Set(tc.key, []byte("opaque recovery evidence")))
			require.NoError(t, batch.Commit(true))
			require.NoError(t, batch.Close())
			require.NoError(t, eng.Close())
			read, err := inspect.OpenStore(inspect.Options{MetaPath: opts.MetaPath, MessagePath: opts.MessagePath, HashSlotCount: 256})
			require.NoError(t, err)
			defer read.Close()
			out := filepath.Join(t.TempDir(), "bundle")
			stats, err := ExportBundle(context.Background(), out, read, ExportOptions{HashSlotCount: 256})
			require.ErrorIs(t, err, ErrValidation)
			require.Contains(t, err.Error(), "MQTT state")
			require.Equal(t, ExportStats{}, stats)
			_, err = os.Stat(out)
			require.True(t, os.IsNotExist(err), "refusal created an output directory: %v", err)
		})
	}
}

func TestExportBundleCancellationDoesNotPrepareOutput(t *testing.T) {
	store, opts := openExportNodeStore(t, t.TempDir())
	require.NoError(t, store.Close())
	read, err := inspect.OpenStore(inspect.Options{MetaPath: opts.MetaPath, MessagePath: opts.MessagePath, HashSlotCount: 256})
	require.NoError(t, err)
	defer read.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := filepath.Join(t.TempDir(), "bundle")
	_, err = ExportBundle(ctx, out, read, ExportOptions{HashSlotCount: 256})
	require.True(t, errors.Is(err, context.Canceled), "error = %v", err)
	_, err = os.Stat(out)
	require.True(t, os.IsNotExist(err), "canceled export prepared output: %v", err)
	// The same empty source still exports normally after the canceled attempt.
	_, err = ExportBundle(context.Background(), out, read, ExportOptions{HashSlotCount: 256})
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(out, "manifest.json"))
	require.NoError(t, err)
}
