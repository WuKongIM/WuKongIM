package replica

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

func TestNewReplicaValidatesRequiredDependencies(t *testing.T) {
	_, err := NewReplica(ReplicaConfig{})
	if !errors.Is(err, channel.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestNewReplicaRejectsInvalidExecutionMode(t *testing.T) {
	env := newTestEnv(t)
	cfg := env.config()
	cfg.Execution.Mode = ExecutionMode("bogus")

	_, err := NewReplica(cfg)
	if !errors.Is(err, channel.ErrInvalidConfig) {
		t.Fatalf("NewReplica() error = %v, want invalid config", err)
	}
}

func TestNewReplicaRejectsInvalidExecutionLimits(t *testing.T) {
	tests := []struct {
		name string
		edit func(*ReplicaConfig)
	}{
		{
			name: "negative mailbox size",
			edit: func(cfg *ReplicaConfig) {
				cfg.Execution.MailboxSize = -1
			},
		},
		{
			name: "negative turn budget",
			edit: func(cfg *ReplicaConfig) {
				cfg.Execution.TurnBudget = -1
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t)
			cfg := env.config()
			tt.edit(&cfg)

			_, err := NewReplica(cfg)
			if !errors.Is(err, channel.ErrInvalidConfig) {
				t.Fatalf("NewReplica() error = %v, want invalid config", err)
			}
		})
	}
}

func TestNewReplicaDefaultsExecutionModeToDedicated(t *testing.T) {
	env := newTestEnv(t)
	cfg := env.config()

	got, err := NewReplica(cfg)
	if err != nil {
		t.Fatalf("NewReplica() error = %v", err)
	}
	t.Cleanup(func() { _ = got.Close() })
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
