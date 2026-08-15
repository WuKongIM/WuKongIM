package cluster

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestPersonDirectoryBatcherCoalescesConcurrentChannelsIntoTwoDurablePhases(t *testing.T) {
	node := &recordingPersonDirectoryBatchNode{}
	batcher := newPersonDirectoryBatcher(node)
	batcher.collectWait = time.Hour
	batcher.targetItems = 4

	errCh := make(chan error, 4)
	for i := 0; i < 4; i++ {
		index := i
		go func() {
			errCh <- batcher.ensure(context.Background(), testPersonDirectoryMutation(index))
		}()
	}
	for range 4 {
		if err := <-errCh; err != nil {
			t.Fatalf("ensure() error = %v", err)
		}
	}

	node.mu.Lock()
	defer node.mu.Unlock()
	if node.membershipCalls != 1 || len(node.memberships) != 8 {
		t.Fatalf("membership calls/rows = %d/%d, want 1/8", node.membershipCalls, len(node.memberships))
	}
	if node.readyCalls != 1 || len(node.ready) != 4 {
		t.Fatalf("ready calls/rows = %d/%d, want 1/4", node.readyCalls, len(node.ready))
	}
}

func TestPersonDirectoryBatcherDoesNotPublishReadyAfterMembershipFailure(t *testing.T) {
	membershipErr := errors.New("membership failed")
	node := &recordingPersonDirectoryBatchNode{membershipErr: membershipErr}
	batcher := newPersonDirectoryBatcher(node)
	batcher.collectWait = time.Millisecond
	batcher.targetItems = 8

	err := batcher.ensure(context.Background(), testPersonDirectoryMutation(0))
	if !errors.Is(err, membershipErr) {
		t.Fatalf("ensure() error = %v, want membership failure", err)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.readyCalls != 0 {
		t.Fatalf("ready calls = %d, want 0 after membership failure", node.readyCalls)
	}
}

func testPersonDirectoryMutation(index int) personDirectoryMutation {
	channelID := string(rune('a'+index)) + "@z"
	return personDirectoryMutation{
		key: metadb.ChannelKey{ChannelID: channelID, ChannelType: 1},
		memberships: []metadb.UserChannelMembership{
			{UID: string(rune('a' + index)), ChannelID: channelID, ChannelType: 1, JoinSeq: 1, SourceVersion: 1, UpdatedAt: 1},
			{UID: "z", ChannelID: channelID, ChannelType: 1, JoinSeq: 1, SourceVersion: 1, UpdatedAt: 1},
		},
	}
}

type recordingPersonDirectoryBatchNode struct {
	mu sync.Mutex

	membershipCalls int
	memberships     []metadb.UserChannelMembership
	membershipErr   error
	readyCalls      int
	ready           []metadb.ChannelKey
}

func (n *recordingPersonDirectoryBatchNode) UpsertUserChannelMembershipBatch(_ context.Context, memberships []metadb.UserChannelMembership) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.membershipCalls++
	n.memberships = append(n.memberships, memberships...)
	return n.membershipErr
}

func (n *recordingPersonDirectoryBatchNode) EnsureChannelDirectoriesReady(_ context.Context, ready []metadb.ChannelKey) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.readyCalls++
	n.ready = append(n.ready, ready...)
	return nil
}
