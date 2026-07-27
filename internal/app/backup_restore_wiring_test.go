package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	backupkeys "github.com/WuKongIM/WuKongIM/pkg/backup/keypackage"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

func TestWireRestoreBuildsFailClosedCompositionAndRegistersRPCs(t *testing.T) {
	root := t.TempDir()
	primary, err := backupinfra.NewFileRepository(
		"primary", filepath.Join(root, "primary"),
	)
	if err != nil {
		t.Fatalf("NewFileRepository(primary): %v", err)
	}
	secondary, err := backupinfra.NewFileRepository(
		"secondary", filepath.Join(root, "secondary"),
	)
	if err != nil {
		t.Fatalf("NewFileRepository(secondary): %v", err)
	}
	node := &restoreWiringNode{
		rpcs: make(map[uint8]cluster.NodeRPCHandler),
	}
	crypto := restoreWiringCrypto{}
	staging := filepath.Join(root, "staging")
	app := &App{
		cfg: Config{
			DataDir: filepath.Join(root, "data"),
			Backup: BackupConfig{
				RestoreMode: true, RepositoryID: "repository-test",
				TargetGeneration: "target-generation-test",
				StagingDir:       staging, StagingMaxBytes: 64 << 20,
				WorkerCount: 2,
			},
		},
		cluster: node,
		logger:  wklog.NewNop(),
	}
	app.wireRestore(
		cluster.Config{
			Control: cluster.ControlConfig{ClusterID: "cluster-target"},
			Slots:   cluster.SlotConfig{HashSlotCount: 1},
		},
		primary, secondary, crypto, crypto,
		backupartifact.NewObjectCodec(
			crypto, bytes.NewReader(bytes.Repeat([]byte{1}, 128)),
		),
		nil,
	)
	if app.backupInitErr != nil {
		t.Fatalf("wireRestore() error = %v", app.backupInitErr)
	}
	if app.restore == nil || app.restoreRuntime == nil {
		t.Fatal("wireRestore() did not build restore app/runtime")
	}
	for _, serviceID := range []uint8{
		accessnode.BackupRestoreTargetRPCServiceID,
		accessnode.BackupRestoreInstallRPCServiceID,
		accessnode.BackupCheckpointReplicaRPCServiceID,
	} {
		if node.rpcs[serviceID] == nil {
			t.Fatalf("RPC service %d is not registered", serviceID)
		}
	}
	if len(node.rpcs) != 3 {
		t.Fatalf("registered RPCs = %d, want 3", len(node.rpcs))
	}
}

func TestRestoreStartupWaitsForDesignatedKeyPinPublisher(t *testing.T) {
	calls := 0
	app := &App{
		backupKeyStartupCheck: func(context.Context) error {
			calls++
			if calls == 1 {
				return backupkeys.ErrRepositoryPinPending
			}
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, app.waitBackupKeyStartupCheck(ctx))
	require.Equal(t, 2, calls)
}

func TestBackupKeyPinPublisherIsStableAcrossLeaderTerms(t *testing.T) {
	publisher := backupKeyPinPublisherNodeID(cluster.Config{
		NodeID: 7,
		Control: cluster.ControlConfig{Voters: []cluster.ControlVoter{
			{NodeID: 7},
			{NodeID: 2},
			{NodeID: 5},
		}},
	})
	require.Equal(t, uint64(2), publisher)
}

func TestBackupKeyPinPublisherRejectsSeedJoinMirror(t *testing.T) {
	publisher := backupKeyPinPublisherNodeID(cluster.Config{
		NodeID:     9,
		ListenAddr: "127.0.0.1:11110",
		Control: cluster.ControlConfig{
			Role: cluster.ControlRoleMirror,
		},
		Join: cluster.JoinConfig{
			Seeds:         []string{"1@127.0.0.1:11111"},
			AdvertiseAddr: "127.0.0.1:11110",
			Token:         "join-token",
		},
	})
	require.Zero(t, publisher)
}

func TestBackupKeyPinPublisherDefaultsImplicitSingleNodeCluster(t *testing.T) {
	publisher := backupKeyPinPublisherNodeID(cluster.Config{
		NodeID:     9,
		ListenAddr: "127.0.0.1:11110",
	})
	require.Equal(t, uint64(9), publisher)
}

type restoreWiringNode struct {
	appRestoreNode
	rpcs map[uint8]cluster.NodeRPCHandler
}

func (*restoreWiringNode) Start(context.Context) error { return nil }
func (*restoreWiringNode) Stop(context.Context) error  { return nil }
func (*restoreWiringNode) NodeID() uint64              { return 1 }
func (n *restoreWiringNode) RegisterRPC(
	serviceID uint8,
	handler cluster.NodeRPCHandler,
) {
	n.rpcs[serviceID] = handler
}

type restoreWiringCrypto struct{}

func (restoreWiringCrypto) NewDataKey(
	_ context.Context,
) (backupartifact.DataKey, error) {
	return backupartifact.DataKey{
		Plaintext: bytes.Repeat([]byte{7}, 32),
		Envelope: backupartifact.DataKeyEnvelope{
			Version: 1, Algorithm: "TEST", KeyID: "test",
			Nonce: []byte{1}, Value: bytes.Repeat([]byte{9}, 32),
		},
	}, nil
}

func (restoreWiringCrypto) OpenDataKey(
	_ context.Context,
	envelope backupartifact.DataKeyEnvelope,
) ([]byte, error) {
	if len(envelope.Value) == 0 {
		return nil, errors.New("wrapped key is empty")
	}
	return bytes.Repeat([]byte{7}, 32), nil
}

func (restoreWiringCrypto) Sign(
	_ context.Context,
	message []byte,
) (backupartifact.ManifestSignature, error) {
	const keyID = "test-signing"
	sum := sha256.Sum256(append([]byte(keyID), message...))
	return backupartifact.ManifestSignature{
		Algorithm: "test-sha256", KeyID: keyID, Value: sum[:],
	}, nil
}

func (restoreWiringCrypto) Verify(
	_ context.Context,
	signature backupartifact.ManifestSignature,
	message []byte,
) error {
	sum := sha256.Sum256(append([]byte(signature.KeyID), message...))
	if signature.Algorithm != "test-sha256" ||
		!bytes.Equal(signature.Value, sum[:]) {
		return errors.New("signature mismatch")
	}
	return nil
}
