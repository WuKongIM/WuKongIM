package gnet

import (
	goruntime "runtime"
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
	if got := events[0].Capacity; got != actorReadyQueueSize {
		t.Fatalf("first pressure capacity = %d, want %d", got, actorReadyQueueSize)
	}
	if got := events[1].Result; got != "closed" {
		t.Fatalf("second pressure result = %q, want closed", got)
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
