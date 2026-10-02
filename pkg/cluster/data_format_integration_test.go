//go:build integration

package cluster

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/dataformat"
	"github.com/stretchr/testify/require"
)

// TestDataFormatSingleNodeClusterReopen preserves registered history and creator
// provenance, and rejects unregistered durable data without adopting or writing it.
func TestDataFormatSingleNodeClusterReopen(t *testing.T) {
	record := recordNodeRestartEvidence(t)
	for _, registered := range []bool{true, false} {
		name := "registered"
		if !registered {
			name = "unregistered"
		}
		t.Run(name, func(t *testing.T) {
			cfg := Config{NodeID: 1, ListenAddr: freeTCPAddr(t), DataDir: t.TempDir(),
				Control: ControlConfig{ClusterID: "format-reopen"},
				Slots:   SlotConfig{InitialSlotCount: 12, HashSlotCount: 256, ReplicaCount: 1},
				// Report the restarted node within the bounded readiness budget.
				HealthReport: HealthReportConfig{Interval: 100 * time.Millisecond},
				CreatedBy:    dataformat.Build{Program: "wukongim", Version: "creator"}}
			node, err := New(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { stopNodes(t, node) })
			// Twelve physical Slots use the bounded cluster-start helper; this
			// format contract is not a five-second single-Slot startup benchmark.
			startNodes(t, node)
			waitNodeWriteReady(t, node)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			channel := channelruntime.ChannelID{ID: "format-history", Type: 2}
			first, err := node.AppendChannel(ctx, channelruntime.AppendRequest{ChannelID: channel,
				CommitMode: channelruntime.CommitModeQuorum,
				Message:    channelruntime.Message{MessageID: 1001, Payload: []byte("before-reopen")}})
			require.NoError(t, err)
			stopNodes(t, node)
			markerPath := filepath.Join(cfg.DataDir, dataformat.FileName)
			marker, err := os.ReadFile(markerPath)
			require.NoError(t, err)
			if !registered {
				// Removing registration must not authorize adoption of durable data.
				require.NoError(t, os.Remove(markerPath))
				before := stoppedDataFileDigests(t, cfg.DataDir)
				cfg.CreatedBy.Version = "upgraded-server"
				_, err := New(cfg)
				require.ErrorIs(t, err, dataformat.ErrUnsupported)
				require.NoFileExists(t, markerPath)
				require.Equal(t, before, stoppedDataFileDigests(t, cfg.DataDir))
				record("unregistered-rejected-without-writes", node)
				return
			}
			cfg.CreatedBy.Version = "upgraded-server"
			node, err = New(cfg)
			require.NoError(t, err)
			startNodes(t, node)
			waitNodeWriteReady(t, node)
			requireChannelMessage(t, node, channel, first.MessageSeq, 1001, []byte("before-reopen"))
			second, err := node.AppendChannel(ctx, channelruntime.AppendRequest{ChannelID: channel,
				CommitMode: channelruntime.CommitModeQuorum,
				Message:    channelruntime.Message{MessageID: 1002, Payload: []byte("after-reopen")}})
			require.NoError(t, err)
			require.Equal(t, first.MessageSeq+1, second.MessageSeq)
			after, err := os.ReadFile(markerPath)
			require.NoError(t, err)
			require.Equal(t, marker, after)
			record("registered-reopened-history-and-creator-preserved", node)
		})
	}
}

// stoppedDataFileDigests compares opaque fixture files only after every runtime
// has stopped; streaming hashes neither decode native storage nor reopen engines.
func stoppedDataFileDigests(t *testing.T, root string) map[string]string {
	t.Helper()
	digests := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		digests[rel] = fmt.Sprintf("%x", hash.Sum(nil))
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, digests)
	return digests
}
