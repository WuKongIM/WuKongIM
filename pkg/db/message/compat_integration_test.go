//go:build integration

package message

import (
	"sync"
	"testing"
	"time"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func TestCompatChannelStoreAppendUsesCommitCoordinatorAcrossChannels(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer engine.Close()
	engine.ConfigureCommitCoordinator(CommitCoordinatorConfig{FlushWindow: 2 * time.Second, MaxRequests: 2})

	storeA := mustForChannel(t, engine, channel.ChannelKey("coordinator-a:1"), channel.ChannelID{ID: "coordinator-a", Type: 1})
	storeB := mustForChannel(t, engine, channel.ChannelKey("coordinator-b:1"), channel.ChannelID{ID: "coordinator-b", Type: 1})

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	recordA := compatTestRecord(t, 1001, "coordinator-a", "client-a")
	recordB := compatTestRecord(t, 1002, "coordinator-b", "client-b")
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := storeA.Append([]channel.Record{recordA})
		errs <- err
	}()

	select {
	case err := <-errs:
		t.Fatalf("first append completed before a second channel could join the commit batch: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := storeB.Append([]channel.Record{recordB})
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
}
