//go:build integration

package message

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

// A periodic owner must yield to physical writers without losing its exact
// pending proof. Holding the real locks isolates admission from I/O timing.
func TestMQTTStoragePeriodicCancellationSkipsBusyLocks(t *testing.T) {
	for _, name := range []string{"append", "checkpoint", "budget"} {
		t.Run(name, func(t *testing.T) {
			e := openCompatEngine(t)
			s := mustForChannel(t, e, "periodic:1", channel.ChannelID{ID: "periodic", Type: 1})
			defer s.Close()
			m := sealCompatProposalManifest(t, DurableProposalManifest{
				Version: DurableProposalManifestVersion, ChannelEpoch: 1,
				LeaderTerm: 1, FenceVersion: 1, CommandID: [32]byte{1}, LastOffset: 1,
			}, []channel.Record{compatExactTestRecord(t, 1, 1, "periodic", "one")})
			p := &mqttStoragePendingRefund{db: e.db, key: s.log.key, id: s.log.id,
				manifest: m, nonce: 1, bytes: 7, preparation: true}
			b := &mqttStorageBudget{used: 7, pendingRefund: p}
			e.db.mqttStorage = b
			var held *sync.Mutex
			switch name {
			case "append":
				held = &s.log.appendMu
			case "checkpoint":
				held = &s.log.checkpointMu
			case "budget":
				held = &b.mu
			}
			held.Lock()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- e.retryMQTTStorageCancellation(ctx) }()
			select {
			case err := <-result:
				if !errors.Is(err, channel.ErrBackpressured) {
					t.Errorf("busy %s returned %v, want pressure without waiting", name, err)
				}
				held.Unlock()
			case <-ctx.Done():
				t.Errorf("periodic owner waited for busy %s beyond its context", name)
				held.Unlock()
				select {
				case <-result:
				case <-time.After(5 * time.Second):
					t.Fatal("periodic owner did not join after releasing the held lock")
				}
			}
			if b.used != 7 || b.pendingRefund != p {
				t.Fatalf("busy admission changed retained debt/proof: used=%d proof=%p", b.used, b.pendingRefund)
			}
		})
	}
}

func TestMQTTStoragePeriodicCancellationHandsCommitToManagedOwner(t *testing.T) {
	e := openCompatEngine(t)
	s := mustForChannel(t, e, "periodic-slow:1", channel.ChannelID{ID: "periodic-slow", Type: 1})
	defer s.Close()
	m := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 1,
		LeaderTerm: 1, FenceVersion: 1, CommandID: [32]byte{1}, LastOffset: 1,
	}, []channel.Record{compatExactTestRecord(t, 1, 1, "periodic-slow", "one")})
	p := &mqttStoragePendingRefund{db: e.db, key: s.log.key, id: s.log.id,
		manifest: m, nonce: 1, bytes: 7, preparation: true}
	b := &mqttStorageBudget{used: 7, pendingRefund: p}
	e.db.mqttStorage = b
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	e.committer.SetCommitFunc(func(batch *engine.Batch) error {
		close(started)
		<-release
		return batch.Commit(true)
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- e.retryMQTTStorageCancellation(ctx) }()
	select {
	case <-started:
	case err := <-result:
		t.Fatalf("periodic cancellation bypassed the managed physical owner: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("periodic cancellation did not enter the managed physical owner")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("slow physical commit returned %v, want retained unknown deadline", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("periodic caller waited for uninterruptible physical commit")
	}
	if b.used != 7 || b.pendingRefund != p {
		t.Fatalf("unknown commit returned credit before proof: used=%d proof=%p", b.used, b.pendingRefund)
	}
	unblock()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if b.used != 0 || b.pendingRefund != nil {
		t.Fatalf("joined committed cancellation did not settle exactly once: used=%d proof=%p", b.used, b.pendingRefund)
	}
}
