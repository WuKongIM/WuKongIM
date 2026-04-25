package gnet

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
	gnetv2 "github.com/panjf2000/gnet/v2"
)

type listenerRuntime struct {
	opts    transport.ListenerOptions
	handler transport.ConnHandler

	mu         sync.RWMutex
	boundAddr  string
	active     bool
	generation uint64
	conns      map[*connState]struct{}
}

func (r *listenerRuntime) addr() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.boundAddr
}

func (r *listenerRuntime) setAddr(addr string) {
	r.mu.Lock()
	r.boundAddr = addr
	r.mu.Unlock()
}

func (r *listenerRuntime) isActive() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

func (r *listenerRuntime) activate() {
	r.mu.Lock()
	r.active = true
	r.mu.Unlock()
}

func (r *listenerRuntime) deactivateAndSnapshot() []*connState {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.active = false
	r.generation++

	conns := make([]*connState, 0, len(r.conns))
	for state := range r.conns {
		conns = append(conns, state)
	}
	return conns
}

func (r *listenerRuntime) admitConn(state *connState) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.active {
		return false
	}
	if r.conns == nil {
		r.conns = make(map[*connState]struct{})
	}
	state.generation = r.generation
	r.conns[state] = struct{}{}
	return true
}

func (r *listenerRuntime) untrackConn(state *connState) {
	if r == nil || state == nil {
		return
	}

	r.mu.Lock()
	delete(r.conns, state)
	r.mu.Unlock()
}

func (r *listenerRuntime) shouldDispatch(state *connState) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active && r.generation == state.generation
}

func (r *listenerRuntime) reportError(err error) {
	if r == nil || err == nil || r.opts.OnError == nil {
		return
	}
	r.opts.OnError(err)
}

type engineCycle struct {
	bootOnce sync.Once
	bootCh   chan error
	doneCh   chan error
}

func newEngineCycle() *engineCycle {
	return &engineCycle{
		bootCh: make(chan error, 1),
		doneCh: make(chan error, 1),
	}
}

func (c *engineCycle) signalBoot(err error) {
	c.bootOnce.Do(func() {
		c.bootCh <- err
		close(c.bootCh)
	})
}

type engineGroup struct {
	gnetv2.BuiltinEventEngine

	mu            sync.Mutex
	runtimes      []*listenerRuntime
	routes        map[string]*listenerRuntime
	engine        gnetv2.Engine
	cycle         *engineCycle
	running       bool
	transitioning bool
	transitionCh  chan struct{}
	bootRuntimes  []*listenerRuntime
	stopEngineFn  func(engine gnetv2.Engine, cycle *engineCycle) error

	nextConnID atomic.Uint64
}

func newEngineGroup(specs []transport.ListenerSpec) *engineGroup {
	runtimes := make([]*listenerRuntime, 0, len(specs))
	for _, spec := range specs {
		runtimes = append(runtimes, &listenerRuntime{
			opts:    spec.Options,
			handler: spec.Handler,
			conns:   make(map[*connState]struct{}),
		})
	}

	return &engineGroup{
		runtimes: runtimes,
		routes:   make(map[string]*listenerRuntime, len(runtimes)),
	}
}

func (g *engineGroup) start(runtime *listenerRuntime) error {
	if runtime == nil {
		return fmt.Errorf("gateway/transport/gnet: missing listener runtime")
	}

	runtime.activate()

	for {
		g.mu.Lock()
		if g.transitioning {
			wait := g.transitionCh
			g.mu.Unlock()
			<-wait
			continue
		}

		if g.running {
			if g.isBoundLocked(runtime) {
				g.mu.Unlock()
				return nil
			}
			g.mu.Unlock()

			// Adding a listener after the shared engine is already serving may require
			// reconciling the bound listener set. Gateway startup starts all configured
			// listeners before serving traffic, so this path is a late-start edge case.
			if err := preflightListenTCP(runtime.opts.Address); err != nil {
				runtime.deactivateAndSnapshot()
				return err
			}

			g.mu.Lock()
			if g.transitioning {
				wait := g.transitionCh
				g.mu.Unlock()
				<-wait
				continue
			}
			if g.isBoundLocked(runtime) {
				g.mu.Unlock()
				return nil
			}

			previous := g.boundRuntimesLocked()
			desired := append([]*listenerRuntime(nil), previous...)
			desired = append(desired, runtime)

			g.transitioning = true
			g.transitionCh = make(chan struct{})
			engine := g.engine
			cycle := g.cycle
			g.mu.Unlock()

			err := g.restartEngine(engine, cycle, desired, previous)
			if err != nil {
				runtime.deactivateAndSnapshot()
			}

			g.mu.Lock()
			close(g.transitionCh)
			g.transitioning = false
			g.mu.Unlock()
			return err
		}

		g.transitioning = true
		g.transitionCh = make(chan struct{})
		desired := g.activeRuntimesLocked()
		g.mu.Unlock()

		err := g.startEngine(desired)
		if err != nil {
			runtime.deactivateAndSnapshot()
		}

		g.mu.Lock()
		if err == nil {
			g.running = true
		}
		close(g.transitionCh)
		g.transitioning = false
		g.mu.Unlock()
		return err
	}
}

func (g *engineGroup) stop(runtime *listenerRuntime) error {
	if runtime == nil {
		return nil
	}

	conns := runtime.deactivateAndSnapshot()
	for _, state := range conns {
		_ = state.raw.Close()
	}

	for {
		g.mu.Lock()
		if g.transitioning {
			wait := g.transitionCh
			g.mu.Unlock()
			<-wait
			continue
		}
		if g.hasActiveBoundRuntimeLocked() {
			g.mu.Unlock()
			return nil
		}
		if !g.running {
			g.mu.Unlock()
			return nil
		}

		g.transitioning = true
		g.transitionCh = make(chan struct{})
		engine := g.engine
		cycle := g.cycle
		g.mu.Unlock()

		err := g.stopEngine(engine, cycle)

		g.mu.Lock()
		if err == nil && g.engine == engine && g.cycle == cycle {
			g.engine = gnetv2.Engine{}
			g.cycle = nil
			g.routes = make(map[string]*listenerRuntime, len(g.runtimes))
			g.bootRuntimes = nil
			g.running = false
		}
		close(g.transitionCh)
		g.transitioning = false
		g.mu.Unlock()
		return err
	}
}

func (g *engineGroup) startEngine(runtimes []*listenerRuntime) error {
	cycle := newEngineCycle()

	g.mu.Lock()
	g.cycle = cycle
	g.bootRuntimes = append([]*listenerRuntime(nil), runtimes...)
	g.routes = make(map[string]*listenerRuntime, len(runtimes))
	g.mu.Unlock()

	addrs := make([]string, 0, len(runtimes))
	for _, runtime := range runtimes {
		addrs = append(addrs, "tcp://"+runtime.opts.Address)
	}

	go func() {
		err := gnetv2.Rotate(g, addrs)
		cycle.signalBoot(err)
		cycle.doneCh <- err
		close(cycle.doneCh)
	}()

	if err := <-cycle.bootCh; err != nil {
		g.mu.Lock()
		if g.cycle == cycle {
			g.cycle = nil
			g.bootRuntimes = nil
			g.routes = make(map[string]*listenerRuntime, len(g.runtimes))
			g.engine = gnetv2.Engine{}
			g.running = false
		}
		g.mu.Unlock()
		return err
	}

	g.mu.Lock()
	if g.cycle == cycle {
		g.bootRuntimes = nil
	}
	g.mu.Unlock()
	return nil
}

func (g *engineGroup) restartEngine(engine gnetv2.Engine, cycle *engineCycle, desired []*listenerRuntime, rollback []*listenerRuntime) error {
	if err := g.stopEngine(engine, cycle); err != nil {
		return err
	}
	if err := g.startEngine(desired); err != nil {
		if len(rollback) > 0 {
			rollbackErr := g.startEngine(rollback)
			if rollbackErr != nil {
				return fmt.Errorf("gateway/transport/gnet: restart failed: %w (rollback failed: %v)", err, rollbackErr)
			}
			g.mu.Lock()
			g.running = true
			g.mu.Unlock()
		}
		return err
	}

	g.mu.Lock()
	g.running = true
	g.mu.Unlock()
	return nil
}

func (g *engineGroup) stopEngine(engine gnetv2.Engine, cycle *engineCycle) error {
	if g.stopEngineFn != nil {
		return g.stopEngineFn(engine, cycle)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := engine.Stop(ctx)
	cancel()

	if cycle != nil {
		if runErr, ok := <-cycle.doneCh; ok && err == nil {
			err = runErr
		}
	}
	return err
}

func (g *engineGroup) OnBoot(engine gnetv2.Engine) (action gnetv2.Action) {
	g.mu.Lock()
	g.engine = engine
	cycle := g.cycle
	runtimes := append([]*listenerRuntime(nil), g.bootRuntimes...)
	g.mu.Unlock()

	routes, err := g.resolveRoutes(engine, runtimes)
	if err != nil {
		if cycle != nil {
			cycle.signalBoot(err)
		}
		g.reportGroupError(runtimes, err)
		return gnetv2.Shutdown
	}

	g.mu.Lock()
	g.routes = routes
	g.mu.Unlock()

	if cycle != nil {
		cycle.signalBoot(nil)
	}
	return gnetv2.None
}

func (g *engineGroup) OnOpen(c gnetv2.Conn) (out []byte, action gnetv2.Action) {
	runtime := g.runtimeByAddr(c.LocalAddr().String())
	if runtime == nil {
		return nil, gnetv2.Close
	}

	state := newConnState(g.nextConnID.Add(1), c, runtime)
	if !runtime.admitConn(state) {
		return nil, gnetv2.Close
	}

	c.SetContext(state)
	state.start()
	if runtime.opts.Network == "tcp" {
		state.enqueueOpen()
	}
	return nil, gnetv2.None
}

func (g *engineGroup) OnTraffic(c gnetv2.Conn) (action gnetv2.Action) {
	state, ok := c.Context().(*connState)
	if !ok || state == nil {
		return gnetv2.Close
	}
	if !state.runtime.shouldDispatch(state) {
		_ = c.Close()
		return gnetv2.None
	}

	if state.currentMode() == connModeTCP {
		buf, err := c.Next(-1)
		if err != nil {
			state.enqueueClose(err)
			_ = c.Close()
			return gnetv2.None
		}
		if len(buf) == 0 {
			return gnetv2.None
		}

		payload := append([]byte(nil), buf...)
		state.enqueueData(payload)
		return gnetv2.None
	}

	return g.handleWSTraffic(c, state)
}

func (g *engineGroup) handleWSTraffic(c gnetv2.Conn, state *connState) gnetv2.Action {
	buf, err := c.Next(-1)
	if err != nil {
		state.enqueueClose(err)
		_ = c.Close()
		return gnetv2.None
	}
	if len(buf) == 0 {
		return gnetv2.None
	}

	state.appendWSInbound(append([]byte(nil), buf...))

	for {
		switch state.currentMode() {
		case connModeWSHandshake:
			result, failure, complete := state.consumeWSHandshake()
			if !complete {
				return gnetv2.None
			}
			if failure != nil {
				transport.LogConnectFailure(state.runtime.opts, state.id, state.localAddr, state.remoteAddr, failure.err)
				state.runtime.reportError(failure.err)
				if len(failure.response) == 0 {
					_ = c.Close()
					return gnetv2.None
				}
				if err := c.AsyncWrite(failure.response, func(conn gnetv2.Conn, err error) error {
					return conn.Close()
				}); err != nil {
					_ = c.Close()
				}
				return gnetv2.None
			}
			if err := c.AsyncWrite(result.response, nil); err != nil {
				_ = c.Close()
				return gnetv2.None
			}
			state.enqueueOpen()
		case connModeWSFrames:
			result, ok := state.nextWSResult()
			if !ok {
				return gnetv2.None
			}

			if len(result.write) > 0 {
				if err := c.AsyncWrite(result.write, nil); err != nil {
					state.enqueueClose(err)
					_ = c.Close()
					return gnetv2.None
				}
			}
			if len(result.payload) > 0 {
				state.enqueueDataWithOpcode(result.opcode, result.payload)
			}
			if len(result.closeWrite) > 0 {
				if err := c.AsyncWrite(result.closeWrite, func(conn gnetv2.Conn, err error) error {
					return conn.Close()
				}); err != nil {
					state.enqueueClose(result.closeErr)
					_ = c.Close()
				}
				return gnetv2.None
			}
			if result.closeNow {
				state.enqueueClose(result.closeErr)
				_ = c.Close()
				return gnetv2.None
			}
		default:
			_ = c.Close()
			return gnetv2.None
		}
	}
}

func (g *engineGroup) OnClose(c gnetv2.Conn, err error) (action gnetv2.Action) {
	state, _ := c.Context().(*connState)
	if state != nil {
		state.runtime.untrackConn(state)
		state.enqueueClose(err)
	}
	return gnetv2.None
}

func (g *engineGroup) resolveRoutes(engine gnetv2.Engine, runtimes []*listenerRuntime) (map[string]*listenerRuntime, error) {
	routes := make(map[string]*listenerRuntime, len(runtimes))
	for _, runtime := range runtimes {
		addr, err := g.resolveRuntimeAddr(engine, runtime)
		if err != nil {
			return nil, err
		}
		runtime.setAddr(addr)
		routes[runtime.opts.Address] = runtime
		routes[addr] = runtime
	}
	return routes, nil
}

func (g *engineGroup) resolveRuntimeAddr(engine gnetv2.Engine, runtime *listenerRuntime) (string, error) {
	fd, err := engine.DupListener("tcp", runtime.opts.Address)
	if err != nil {
		return "", fmt.Errorf("gateway/transport/gnet: dup listener %q: %w", runtime.opts.Address, err)
	}

	file := os.NewFile(uintptr(fd), runtime.opts.Name)
	if file == nil {
		return "", fmt.Errorf("gateway/transport/gnet: dup listener %q returned nil file", runtime.opts.Address)
	}
	defer file.Close()

	ln, err := net.FileListener(file)
	if err != nil {
		return "", fmt.Errorf("gateway/transport/gnet: resolve listener %q: %w", runtime.opts.Address, err)
	}
	defer ln.Close()

	if ln.Addr() == nil {
		return "", fmt.Errorf("gateway/transport/gnet: listener %q has no bound address", runtime.opts.Address)
	}
	return ln.Addr().String(), nil
}

func (g *engineGroup) runtimeByAddr(addr string) *listenerRuntime {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.routes[addr]
}

func (g *engineGroup) reportGroupError(runtimes []*listenerRuntime, err error) {
	if err == nil {
		return
	}
	for _, runtime := range runtimes {
		if runtime.opts.OnError != nil {
			runtime.opts.OnError(err)
		}
	}
}

func (g *engineGroup) activeRuntimesLocked() []*listenerRuntime {
	active := make([]*listenerRuntime, 0, len(g.runtimes))
	for _, runtime := range g.runtimes {
		if runtime.isActive() {
			active = append(active, runtime)
		}
	}
	return active
}

func (g *engineGroup) boundRuntimesLocked() []*listenerRuntime {
	bound := make([]*listenerRuntime, 0, len(g.runtimes))
	seen := make(map[*listenerRuntime]struct{}, len(g.runtimes))
	for _, runtime := range g.routes {
		if _, ok := seen[runtime]; ok {
			continue
		}
		seen[runtime] = struct{}{}
		bound = append(bound, runtime)
	}
	return bound
}

func (g *engineGroup) isBoundLocked(runtime *listenerRuntime) bool {
	if runtime == nil {
		return false
	}
	route, ok := g.routes[runtime.opts.Address]
	return ok && route == runtime
}

func (g *engineGroup) hasActiveBoundRuntimeLocked() bool {
	for _, runtime := range g.boundRuntimesLocked() {
		if runtime.isActive() {
			return true
		}
	}
	return false
}

func preflightListenTCP(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return ln.Close()
}
