//go:build integration

package persondirectory

import (
	"context"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// Direct work must succeed even when recovery discovery has no pages. A failed
// UID write must never acknowledge readiness; retry must retain the task tail.
func TestPrepareBypassesRecoveryScanAndWaitsForDurableCompletion(t *testing.T) {
	task := metadb.PersonDirectoryTask{ChannelID: runtimechannelid.EncodePersonChannel("u1", "u2"), ChannelType: 1, Generation: 7, CommittedTail: 9, CreatedAt: 123}
	source := &directOnlySource{projectorTaskSource: newProjectorTaskSource(7, task)}
	writer := &partialMembershipWriter{calls: make(chan []metadb.UserChannelMembership, 4), fail: true}
	p, err := New(Options{Source: source, Memberships: writer})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer p.Stop(context.Background())
	items := []metadb.PersonDirectoryTaskEntry{{HashSlot: 7, Task: task}}
	if got := p.Prepare(ctx, items); len(got) != 1 || got[0] == nil {
		t.Fatalf("failed projection acknowledged: %v", got)
	}
	assertProjectedMemberships(t, <-writer.calls, task)
	if source.completeCount() != 0 {
		t.Fatal("failed projection completed")
	}
	writer.setFail(false)
	if got := p.Prepare(ctx, items); len(got) != 1 || got[0] != nil {
		t.Fatalf("prepare: %v", got)
	}
	assertProjectedMemberships(t, <-writer.calls, task)
	if source.completeCount() != 1 {
		t.Fatal("readiness returned before durable completion")
	}
}

type directOnlySource struct{ *projectorTaskSource }

func (*directOnlySource) LocalLeaderHashSlots(context.Context) ([]metadb.HashSlot, error) {
	return nil, nil
}
