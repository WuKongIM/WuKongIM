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
	if node.prepareCalls != 1 || len(node.memberships) != 8 || len(node.preparedChannels) != 4 {
		t.Fatalf("prepare calls/membership rows/channels = %d/%d/%d, want 1/8/4", node.prepareCalls, len(node.memberships), len(node.preparedChannels))
	}
	if node.readyCalls != 1 || len(node.ready) != 4 {
		t.Fatalf("ready calls/rows = %d/%d, want 1/4", node.readyCalls, len(node.ready))
	}
}

func TestPersonDirectoryBatcherDoesNotPublishReadyAfterMembershipFailure(t *testing.T) {
	membershipErr := errors.New("membership failed")
	node := &recordingPersonDirectoryBatchNode{prepareErr: membershipErr}
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

func TestPersonDirectoryBatcherWaitsForCapacityInsteadOfRejectingColdWave(t *testing.T) {
	const queuedItems = 32
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)

	node := &blockingPersonDirectoryBatchNode{release: release}
	batcher := newPersonDirectoryBatcher(node)
	batcher.collectWait = time.Hour
	batcher.targetItems = 1
	batcher.maxQueued = queuedItems

	results := make(chan error, queuedItems+1)
	for index := 0; index < queuedItems; index++ {
		index := index
		go func() {
			results <- batcher.ensure(context.Background(), testPersonDirectoryMutation(index))
		}()
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		batcher.mu.Lock()
		queued := batcher.queuedItems
		batcher.mu.Unlock()
		if queued == queuedItems {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued person directories = %d, want %d", queued, queuedItems)
		}
		time.Sleep(time.Millisecond)
	}

	extra := make(chan error, 1)
	go func() {
		extra <- batcher.ensure(context.Background(), testPersonDirectoryMutation(queuedItems))
	}()
	select {
	case err := <-extra:
		releaseAll()
		for range queuedItems {
			<-results
		}
		t.Fatalf("extra ensure returned %v while the bounded queue was transiently full; want it to wait", err)
	case <-time.After(50 * time.Millisecond):
	}

	releaseAll()
	for range queuedItems {
		if err := <-results; err != nil {
			t.Fatalf("queued ensure error = %v", err)
		}
	}
	if err := <-extra; err != nil {
		t.Fatalf("extra ensure after capacity release error = %v", err)
	}
}

func TestPersonDirectoryBatcherRunsEightColdDirectoryBatchesConcurrently(t *testing.T) {
	const concurrentBatches = 8
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)

	node := &blockingPersonDirectoryBatchNode{
		started: make(chan struct{}, concurrentBatches),
		release: release,
	}
	batcher := newPersonDirectoryBatcher(node)
	batcher.collectWait = time.Hour
	batcher.targetItems = 1

	results := make(chan error, concurrentBatches)
	for index := 0; index < concurrentBatches; index++ {
		index := index
		go func() {
			results <- batcher.ensure(context.Background(), testPersonDirectoryMutation(index))
		}()
		select {
		case <-node.started:
		case <-time.After(time.Second):
			releaseAll()
			for range index + 1 {
				<-results
			}
			t.Fatalf("active person-directory batches = %d, want %d", index, concurrentBatches)
		}
	}
	releaseAll()
	for range concurrentBatches {
		if err := <-results; err != nil {
			t.Fatalf("ensure() error = %v", err)
		}
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

	prepareCalls     int
	memberships      []metadb.UserChannelMembership
	preparedChannels []metadb.ChannelKey
	prepareErr       error
	readyCalls       int
	ready            []metadb.ChannelKey
}

type blockingPersonDirectoryBatchNode struct {
	started chan struct{}
	release <-chan struct{}
}

func (n *blockingPersonDirectoryBatchNode) PreparePersonChannelDirectoryBatch(ctx context.Context, _ []metadb.UserChannelMembership, _ []metadb.ChannelKey) error {
	if n.started != nil {
		n.started <- struct{}{}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-n.release:
		return nil
	}
}

func (n *blockingPersonDirectoryBatchNode) EnsureChannelDirectoriesReady(context.Context, []metadb.ChannelKey) error {
	return nil
}

func (n *recordingPersonDirectoryBatchNode) PreparePersonChannelDirectoryBatch(_ context.Context, memberships []metadb.UserChannelMembership, channels []metadb.ChannelKey) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.prepareCalls++
	n.memberships = append(n.memberships, memberships...)
	n.preparedChannels = append(n.preparedChannels, channels...)
	return n.prepareErr
}

func (n *recordingPersonDirectoryBatchNode) EnsureChannelDirectoriesReady(_ context.Context, ready []metadb.ChannelKey) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.readyCalls++
	n.ready = append(n.ready, ready...)
	return nil
}
