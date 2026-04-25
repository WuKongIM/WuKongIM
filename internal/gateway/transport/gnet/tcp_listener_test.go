package gnet

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
	gnetv2 "github.com/panjf2000/gnet/v2"
)

func TestTCPFinalStopErrorPreservesGroupState(t *testing.T) {
	spec := transport.ListenerSpec{Options: transport.ListenerOptions{Name: "tcp-a", Network: "tcp", Address: "127.0.0.1:9100"}, Handler: noopHandler{}}
	group := newEngineGroup([]transport.ListenerSpec{spec})
	runtime := group.runtimes[0]
	runtime.activate()
	runtime.setAddr("127.0.0.1:9100")

	group.running = true
	group.cycle = newEngineCycle()
	group.routes = map[string]*listenerRuntime{
		runtime.opts.Address: runtime,
		runtime.addr():       runtime,
	}

	stopErr := errors.New("stop failed")
	group.stopEngineFn = func(engine gnetv2.Engine, cycle *engineCycle) error {
		return stopErr
	}

	if err := group.stop(runtime); !errors.Is(err, stopErr) {
		t.Fatalf("stop error = %v, want %v", err, stopErr)
	}

	if !group.running {
		t.Fatal("group marked not running after stop error")
	}
	if group.cycle == nil {
		t.Fatal("group cycle cleared after stop error")
	}
	if len(group.routes) == 0 {
		t.Fatal("group routes cleared after stop error")
	}
	if got := runtime.isActive(); got {
		t.Fatal("runtime remained active after stop")
	}
}
