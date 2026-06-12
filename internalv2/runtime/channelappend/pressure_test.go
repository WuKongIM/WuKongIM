package channelappend

import (
	"context"
	"sync"
	"testing"
	"time"
)

type capturePressureObserver struct {
	mu        sync.Mutex
	seen      bool
	last      WriterPressureObservation
	antsSeen  map[string]AntsPoolObservation
	antsReady chan struct{}
	ready     chan struct{}
}

func newCapturePressureObserver() *capturePressureObserver {
	return &capturePressureObserver{
		antsSeen:  make(map[string]AntsPoolObservation),
		antsReady: make(chan struct{}),
		ready:     make(chan struct{}),
	}
}

func (c *capturePressureObserver) AppendFinished(string, error, time.Duration) {}

func (c *capturePressureObserver) SetChannelAppendWriterPressure(event WriterPressureObservation) {
	c.mu.Lock()
	c.last = event
	if !c.seen && (event.PendingAppendItems > 0 || event.AppendInflightItems > 0) {
		c.seen = true
		close(c.ready)
	}
	c.mu.Unlock()
}

func (c *capturePressureObserver) ObserveChannelAppendAntsPool(event AntsPoolObservation) {
	c.mu.Lock()
	c.antsSeen[event.Pool] = event
	if len(c.antsSeen) >= 2 {
		select {
		case <-c.antsReady:
		default:
			close(c.antsReady)
		}
	}
	c.mu.Unlock()
}

func TestGroupEmitsAggregatePressure(t *testing.T) {
	observer := newCapturePressureObserver()
	appender := newBlockingAppenderForAppendTest()
	group := New(Options{
		LocalNodeID:         1,
		AuthorityShardCount: 4,
		EffectPoolSize:      5,
		MessageID:           newSequenceIDsForPrepare(1),
		Appender:            appender,
		Observer:            observer,
	})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	t.Cleanup(func() {
		for {
			select {
			case started := <-appender.startedC:
				started.Release()
			default:
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := group.Stop(ctx); err != nil {
					t.Fatalf("Stop error = %v", err)
				}
				return
			}
		}
	})

	target := benchmarkAuthorityTarget("p1")
	for i := 0; i < 5; i++ {
		if _, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{benchmarkSendItem("p1")}); err != nil {
			t.Fatalf("SubmitLocal error = %v", err)
		}
	}

	select {
	case <-observer.ready:
	case <-time.After(2 * time.Second):
		observer.mu.Lock()
		last := observer.last
		observer.mu.Unlock()
		t.Fatalf("no pressure observation with pending/in-flight work; last = %#v", last)
	}
	select {
	case <-observer.antsReady:
	case <-time.After(2 * time.Second):
		observer.mu.Lock()
		seen := observer.antsSeen
		observer.mu.Unlock()
		t.Fatalf("no ants pool observations; seen = %#v", seen)
	}
	observer.mu.Lock()
	advance := observer.antsSeen["advance"]
	effect := observer.antsSeen["effect"]
	observer.mu.Unlock()
	if advance.Capacity == 0 || effect.Capacity != 5 {
		t.Fatalf("ants pool observations = advance %#v effect %#v, want advance and effect pools", advance, effect)
	}
}

func TestGroupDisablesWriterPressureMetricsWithoutObserver(t *testing.T) {
	group := New(Options{
		LocalNodeID:         1,
		AuthorityShardCount: 2,
		MessageID:           newSequenceIDsForPrepare(1),
		Appender:            &recordingAppenderForAppendTest{},
		EffectPoolSize:      1,
	})

	for _, shard := range group.shards {
		if shard.ports.metrics != nil {
			t.Fatalf("writer pressure metrics = %p, want nil without pressure observer", shard.ports.metrics)
		}
	}
}

func TestGroupEnablesWriterPressureMetricsWithObserver(t *testing.T) {
	observer := newCapturePressureObserver()
	group := New(Options{
		LocalNodeID:         1,
		AuthorityShardCount: 2,
		MessageID:           newSequenceIDsForPrepare(1),
		Appender:            &recordingAppenderForAppendTest{},
		EffectPoolSize:      1,
		Observer:            observer,
	})

	for _, shard := range group.shards {
		if shard.ports.metrics == nil {
			t.Fatal("writer pressure metrics = nil, want enabled with pressure observer")
		}
	}
}
