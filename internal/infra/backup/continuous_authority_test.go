package backup_test

import (
	"context"
	"errors"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestClusterSlotCaptureAuthorityRequiresExactLocalSlotLeader(t *testing.T) {
	node := &captureAuthorityNode{
		authority: clusterpkg.BackupCaptureAuthority{
			HashSlot: 17, SlotID: 3, HolderNodeID: 2, LeaderTerm: 9, ConfigEpoch: 4,
		},
	}
	source, err := backupinfra.NewClusterSlotCaptureAuthority(node)
	if err != nil {
		t.Fatalf("NewClusterSlotCaptureAuthority() error = %v", err)
	}

	authority, err := source.CurrentCaptureAuthority(context.Background(), 17)
	if err != nil {
		t.Fatalf("CurrentCaptureAuthority() error = %v", err)
	}
	if authority != (runtimebackup.SlotCaptureAuthority{
		SlotID: 3, LeaderTerm: 9, ConfigEpoch: 4, HolderNodeID: 2,
	}) {
		t.Fatalf("authority = %#v", authority)
	}

	node.err = clusterpkg.ErrNotLeader
	if _, err := source.CurrentCaptureAuthority(context.Background(), 17); !errors.Is(err, runtimebackup.ErrCaptureNotLeader) {
		t.Fatalf("remote leader error = %v, want ErrCaptureNotLeader", err)
	}
}

type captureAuthorityNode struct {
	authority clusterpkg.BackupCaptureAuthority
	err       error
}

func (n *captureAuthorityNode) ObserveBackupCaptureAuthority(context.Context, uint16) (clusterpkg.BackupCaptureAuthority, error) {
	return n.authority, n.err
}
