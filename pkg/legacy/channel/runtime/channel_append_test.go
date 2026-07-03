package runtime

import (
	"context"
	"testing"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

func TestChannelAppendUsesOwnedBatchWhenReplicaSupportsIt(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	rep := &fakeReplica{
		state: core.ReplicaState{
			ChannelKey:  "group-10",
			Role:        core.ReplicaRoleLeader,
			CommitReady: true,
		},
	}
	ch := newChannel(
		"group-10",
		1,
		rep,
		core.Meta{
			Key:        "group-10",
			Leader:     1,
			LeaseUntil: now.Add(time.Minute),
		},
		func() time.Time { return now },
		nil,
		nil,
		nil,
	)

	_, err := ch.Append(context.Background(), []core.Record{{Payload: []byte("x"), SizeBytes: 1}})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if rep.appendOwnedCalls != 1 {
		t.Fatalf("AppendOwned() calls = %d, want 1", rep.appendOwnedCalls)
	}
	if rep.appendCalls != 0 {
		t.Fatalf("Append() calls = %d, want 0", rep.appendCalls)
	}
	if rep.appendOwnedRecordCount != 1 {
		t.Fatalf("AppendOwned() record count = %d, want 1", rep.appendOwnedRecordCount)
	}
}
