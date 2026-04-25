package replica

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestNewReplicaValidatesRequiredDependencies(t *testing.T) {
	_, err := NewReplica(ReplicaConfig{})
	if !errors.Is(err, channel.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestReplicaInterfaceSurfaceCompiles(t *testing.T) {
	var r Replica = &replica{}
	_, _ = r.Append(context.Background(), nil)
	_ = r.ApplyMeta(channel.Meta{})
	_ = r.BecomeLeader(channel.Meta{})
	_ = r.BecomeFollower(channel.Meta{})
	_ = r.Tombstone()
	_, _ = r.Fetch(context.Background(), channel.ReplicaFetchRequest{})
	_ = r.ApplyFetch(context.Background(), channel.ReplicaApplyFetchRequest{})
	_ = r.ApplyProgressAck(context.Background(), channel.ReplicaProgressAckRequest{})
	_ = r.InstallSnapshot(context.Background(), channel.Snapshot{})
	_ = r.Close()
	_ = r.Status()
}
