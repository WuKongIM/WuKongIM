package gnet

import (
	goruntime "runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	gnetv2 "github.com/panjf2000/gnet/v2"
)

func TestActorShardSchedulesConnectionOnceWhilePending(t *testing.T) {
	shard := newActorShard(0, nil)
	state := &connState{}

	if !shard.schedule(state) {
		t.Fatal("first schedule returned false, want true")
	}
	if shard.schedule(state) {
		t.Fatal("second schedule returned true, want false while state is already pending")
	}
}

func TestActorShardReportsReadyPressure(t *testing.T) {
	observer := &recordingTransportPressureObserver{}
	runtime := &listenerRuntime{opts: transport.ListenerOptions{Observer: observer}}
	shard := newActorShard(0, nil)
	state := &connState{runtime: runtime}

	if !shard.schedule(state) {
		t.Fatal("schedule returned false, want true")
	}
	shard.markStopped()
	if shard.schedule(&connState{runtime: runtime}) {
		t.Fatal("schedule returned true after shard stopped")
	}

	events := observer.snapshot()
	if len(events) != 2 {
		t.Fatalf("pressure events = %d, want 2", len(events))
	}
	if got := events[0].Name; got != "actor_ready" {
		t.Fatalf("first pressure name = %q, want actor_ready", got)
	}
	if got := events[0].Queue; got != "ready" {
		t.Fatalf("first pressure queue = %q, want ready", got)
	}
	if got := events[0].Result; got != "ok" {
		t.Fatalf("first pressure result = %q, want ok", got)
	}
	if got := events[0].Depth; got != 1 {
		t.Fatalf("first pressure depth = %d, want 1", got)
	}
	if got := events[0].Capacity; got != 0 {
		t.Fatalf("first pressure capacity = %d, want no fixed item bound", got)
	}
	if got := events[1].Result; got != "closed" {
		t.Fatalf("second pressure result = %q, want closed", got)
	}
}

func TestActorPressureRevisionRemainsMonotonicAcrossPoolRestart(t *testing.T) {
	observer := &recordingTransportPressureObserver{}
	runtime := &listenerRuntime{opts: transport.ListenerOptions{Observer: observer}}
	var revision atomic.Uint64

	first := newActorPoolWithRevision(1, &revision)
	firstState := &connState{runtime: runtime}
	if !first.shards[0].schedule(firstState) {
		t.Fatal("first actor pool rejected schedule")
	}
	first.shards[0].drainReady()

	second := newActorPoolWithRevision(1, &revision)
	secondState := &connState{runtime: runtime}
	if !second.shards[0].schedule(secondState) {
		t.Fatal("restarted actor pool rejected schedule")
	}

	events := observer.snapshot()
	if len(events) == 0 {
		t.Fatal("actor pools published no pressure observations")
	}
	if last := events[len(events)-1]; last.Name != "actor_ready" || last.Depth != 1 {
		t.Fatalf("restarted actor pressure = %+v, want ready depth 1", last)
	}
}

func TestGnetActorRuntimeDoesNotSpawnPerConnectionGoroutine(t *testing.T) {
	spec := transport.ListenerSpec{
		Options: transport.ListenerOptions{Name: "tcp-a", Network: "tcp", Address: "local"},
		Handler: noopHandler{},
	}
	group := newEngineGroup([]transport.ListenerSpec{spec})
	runtime := group.runtimes[0]
	runtime.activate()
	runtime.setAddr("local")
	group.routes = map[string]*listenerRuntime{"local": runtime}

	actors := newActorPool(1)
	actors.start()
	group.actors.Store(actors)
	defer actors.stop()

	base := goruntime.NumGoroutine()
	conns := make([]*allocTestGnetConn, 0, 200)
	defer func() {
		for _, conn := range conns {
			group.OnClose(conn, nil)
		}
	}()

	for i := 0; i < cap(conns); i++ {
		conn := &allocTestGnetConn{}
		conns = append(conns, conn)
		_, action := group.OnOpen(conn)
		if action != 0 {
			t.Fatalf("OnOpen action = %v, want none", action)
		}
	}

	deadline := time.Now().Add(200 * time.Millisecond)
	var got int
	for {
		got = goruntime.NumGoroutine()
		if got <= base+20 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got > base+20 {
		t.Fatalf("goroutines after %d conns = %d, want <= %d", len(conns), got, base+20)
	}
}

func TestActorPoolStopsWithEngineGroup(t *testing.T) {
	group := newEngineGroup(nil)
	group.stopEngineFn = func(gnetv2.Engine, *engineCycle) error { return nil }

	actors := newActorPool(1)
	actors.start()
	group.actors.Store(actors)

	if err := group.stopEngine(gnetv2.Engine{}, nil); err != nil {
		t.Fatalf("stopEngine: %v", err)
	}
	if got := group.actors.Load(); got != nil {
		t.Fatal("actor pool remained installed after engine stop")
	}
	if actors.shards[0].schedule(&connState{}) {
		t.Fatal("actor shard accepted schedule after engine stop")
	}
}

// TestActorReadyPressureHasNoFixedItemCapacity exercises actual admission and
// draining without starting an actor worker or depending on elapsed time.
func TestActorReadyPressureHasNoFixedItemCapacity(t *testing.T) {
	for _, scenario := range []string{"above_preallocation", "duplicate", "two_shards", "drained", "late_revision"} {
		t.Run(scenario, func(t *testing.T) {
			observer := &recordingTransportPressureObserver{}
			runtime := &listenerRuntime{opts: transport.ListenerOptions{Observer: observer}}
			pool := newActorPool(2)
			first := &connState{runtime: runtime}
			if !pool.shards[0].schedule(first) {
				t.Fatal("first schedule rejected")
			}
			wantDepth := 1
			switch scenario {
			case "above_preallocation":
				for i := 1; i < 1025; i++ {
					if !pool.shards[0].schedule(&connState{runtime: runtime}) {
						t.Fatal("distinct schedule rejected")
					}
				}
				wantDepth = 1025
			case "duplicate":
				before := len(observer.snapshot())
				if pool.shards[0].schedule(first) {
					t.Fatal("duplicate schedule accepted")
				}
				if got := len(observer.snapshot()); got != before {
					t.Fatalf("duplicate emitted %d observations, want %d", got, before)
				}
			case "two_shards":
				if !pool.shards[1].schedule(&connState{runtime: runtime}) {
					t.Fatal("second shard rejected")
				}
				wantDepth = 2
			case "drained", "late_revision":
				pool.shards[0].drainReady()
				wantDepth = 0
				if scenario == "late_revision" {
					first.observeTransportSnapshotForSource(actorPressureSource(0), transportPressureSnapshot{name: "actor_ready", queue: "ready", depth: 1, revision: 1})
				}
			}
			events := observer.snapshot()
			last := events[len(events)-1]
			if last.Depth != wantDepth {
				t.Fatalf("depth = %d, want %d", last.Depth, wantDepth)
			}
			if last.Capacity != 0 {
				t.Fatalf("capacity = %d, want no fixed item bound", last.Capacity)
			}
		})
	}
}
