package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
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
				SigningKeyID:     "signing-test",
				TargetGeneration: "target-generation-test",
				StagingDir:       staging, StagingMaxBytes: 64 << 20,
				MaxParallelPartitions: 2,
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

func (restoreWiringCrypto) GenerateDataKey(
	_ context.Context,
	_ string,
) (backupartifact.DataKey, error) {
	return backupartifact.DataKey{
		Plaintext: bytes.Repeat([]byte{7}, 32),
		Wrapped:   bytes.Repeat([]byte{9}, 32),
	}, nil
}

func (restoreWiringCrypto) UnwrapDataKey(
	_ context.Context,
	_ string,
	wrapped []byte,
) ([]byte, error) {
	if len(wrapped) == 0 {
		return nil, errors.New("wrapped key is empty")
	}
	return bytes.Repeat([]byte{7}, 32), nil
}

func (restoreWiringCrypto) Sign(
	_ context.Context,
	keyID string,
	message []byte,
) (backupartifact.ManifestSignature, error) {
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
