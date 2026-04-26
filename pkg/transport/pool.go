package transport

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const tcpKeepAlivePeriod = 30 * time.Second
const poolDialCooldown = 50 * time.Millisecond

type PoolConfig struct {
	Discovery   Discovery
	Size        int
	DialTimeout time.Duration
	Dial        func(network, addr string, timeout time.Duration) (net.Conn, error)
	QueueSizes  [numPriorities]int
	DefaultPri  Priority
	Observer    ObserverHooks
}

// Pool manages outbound TCP connections to remote nodes.
type Pool struct {
	cfg    PoolConfig
	size   int
	nodes  sync.Map // NodeID -> *nodeConnSet
	nextID atomic.Uint64
}

type nodeConnSet struct {
	addr    atomic.Value
	evicted atomic.Bool
	slots   []*connSlot
	conns   []atomic.Pointer[MuxConn]
}

type connSlot struct {
	conn         atomic.Pointer[MuxConn]
	mirror       *atomic.Pointer[MuxConn]
	mu           sync.Mutex
	ready        chan struct{}
	lastDialFail time.Time
	lastErr      error
}

func NewPool(args ...any) *Pool {
	var cfg PoolConfig
	switch len(args) {
	case 1:
		cfg = args[0].(PoolConfig)
	case 3:
		cfg = PoolConfig{
			Discovery:   args[0].(Discovery),
			Size:        args[1].(int),
			DialTimeout: args[2].(time.Duration),
		}
	default:
		panic("nodetransport: invalid NewPool arguments")
	}
	if cfg.Size <= 0 {
		cfg.Size = 1
	}
	return &Pool{cfg: cfg, size: cfg.Size}
}

func (p *Pool) Send(nodeID NodeID, shardKey uint64, msgType uint8, body []byte) error {
	mc, err := p.acquire(nodeID, shardKey)
	if err != nil {
		p.observeEnqueue(nodeID, p.cfg.DefaultPri, err)
		return err
	}
	err = mc.Send(p.cfg.DefaultPri, msgType, body)
	p.observeEnqueue(nodeID, p.cfg.DefaultPri, err)
	return err
}

func (p *Pool) RPC(ctx context.Context, nodeID NodeID, shardKey uint64, payload []byte) ([]byte, error) {
	mc, err := p.acquire(nodeID, shardKey)
	if err != nil {
		p.observeEnqueue(nodeID, PriorityRPC, err)
		return nil, err
	}
	reqID := p.nextID.Add(1)
	wire := encodeRPCRequest(reqID, payload)
	resp, err := mc.RPC(ctx, p.cfg.DefaultPri, reqID, wire)
	p.observeEnqueue(nodeID, p.cfg.DefaultPri, err)
	return resp, err
}

func (p *Pool) Close() {
	p.nodes.Range(func(_, value any) bool {
		value.(*nodeConnSet).close()
		return true
	})
}

// ClosePeer evicts all cached connections for one peer without touching other nodes.
func (p *Pool) ClosePeer(nodeID NodeID) {
	if p == nil {
		return
	}
	if value, ok := p.nodes.LoadAndDelete(nodeID); ok {
		value.(*nodeConnSet).evictAndClose()
	}
}

func (p *Pool) Stats() []PoolPeerStats {
	if p == nil {
		return nil
	}

	stats := make([]PoolPeerStats, 0)
	p.nodes.Range(func(key, value any) bool {
		nodeID, ok := key.(NodeID)
		if !ok {
			return true
		}
		set, ok := value.(*nodeConnSet)
		if !ok || set == nil {
			return true
		}
		active := 0
		for i := range set.slots {
			if mc := set.slots[i].conn.Load(); mc != nil && !mc.closed.Load() {
				active++
			}
		}
		idle := len(set.slots) - active
		if idle < 0 {
			idle = 0
		}
		stats = append(stats, PoolPeerStats{
			NodeID: nodeID,
			Active: active,
			Idle:   idle,
		})
		return true
	})
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].NodeID < stats[j].NodeID
	})
	return stats
}

func (p *Pool) acquire(nodeID NodeID, shardKey uint64) (*MuxConn, error) {
	set, err := p.getOrCreateNodeSet(nodeID)
	if err != nil {
		return nil, err
	}
	slot := set.slots[int(shardKey%uint64(len(set.slots)))]
	for {
		if set.evicted.Load() {
			set, err = p.getOrCreateNodeSet(nodeID)
			if err != nil {
				return nil, err
			}
			slot = set.slots[int(shardKey%uint64(len(set.slots)))]
			continue
		}
		if mc := slot.conn.Load(); mc != nil {
			if !mc.closed.Load() {
				return mc, nil
			}
			slot.clearClosedConn(mc)
		}

		ready, cachedErr := slot.waiterOrCooldown()
		if ready != nil {
			<-ready
			continue
		}
		if cachedErr != nil {
			return nil, cachedErr
		}

		ready = make(chan struct{})
		slot.mu.Lock()
		if set.evicted.Load() {
			slot.mu.Unlock()
			continue
		}
		if mc := slot.conn.Load(); mc != nil {
			slot.mu.Unlock()
			continue
		}
		if slot.ready != nil {
			ready = slot.ready
			slot.mu.Unlock()
			<-ready
			continue
		}
		slot.ready = ready
		slot.mu.Unlock()

		addr, resolveErr := p.cfg.Discovery.Resolve(nodeID)
		if resolveErr != nil {
			slot.finishDial(nil, resolveErr, ready)
			return nil, resolveErr
		}
		set.addr.Store(addr)

		dial := p.cfg.Dial
		if dial == nil {
			dial = net.DialTimeout
		}
		startedAt := time.Now()
		raw, dialErr := dial("tcp", addr, p.cfg.DialTimeout)
		p.observeDial(nodeID, dialErr, time.Since(startedAt))
		if dialErr != nil {
			slot.finishDial(nil, dialErr, ready)
			return nil, dialErr
		}
		if set.evicted.Load() {
			_ = raw.Close()
			slot.finishDial(nil, nil, ready)
			continue
		}
		setTCPKeepAlive(raw)
		mc := newMuxConn(raw, nil, ConnConfig{QueueSizes: p.cfg.QueueSizes, Observer: p.cfg.Observer})
		if !slot.finishDialIfActive(mc, ready, func() bool { return !set.evicted.Load() }) {
			continue
		}
		if current, ok := p.nodes.Load(nodeID); !ok || current != set || mc.closed.Load() {
			mc.Close()
			continue
		}
		return mc, nil
	}
}

func (p *Pool) observeDial(nodeID NodeID, err error, dur time.Duration) {
	if p == nil || p.cfg.Observer.OnDial == nil {
		return
	}
	p.cfg.Observer.OnDial(DialEvent{
		TargetNode: nodeID,
		Result:     dialResult(err),
		Duration:   dur,
	})
}

func (p *Pool) observeEnqueue(nodeID NodeID, pri Priority, err error) {
	if p == nil || p.cfg.Observer.OnEnqueue == nil {
		return
	}
	p.cfg.Observer.OnEnqueue(EnqueueEvent{
		TargetNode: nodeID,
		Kind:       priorityKind(pri),
		Result:     enqueueResult(err),
	})
}

func dialResult(err error) string {
	if err == nil {
		return "ok"
	}
	return "dial_error"
}

func enqueueResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrQueueFull):
		return "queue_full"
	case errors.Is(err, ErrStopped):
		return "stopped"
	default:
		return "other"
	}
}

func priorityKind(pri Priority) string {
	switch pri {
	case PriorityRaft:
		return "raft"
	case PriorityRPC:
		return "rpc"
	case PriorityBulk:
		return "bulk"
	default:
		return "unknown"
	}
}

func (p *Pool) getOrCreateNodeSet(nodeID NodeID) (*nodeConnSet, error) {
	for {
		if value, ok := p.nodes.Load(nodeID); ok {
			set := value.(*nodeConnSet)
			if !set.evicted.Load() {
				return set, nil
			}
			p.nodes.CompareAndDelete(nodeID, set)
			continue
		}

		addr, err := p.cfg.Discovery.Resolve(nodeID)
		if err != nil {
			return nil, err
		}

		created := &nodeConnSet{
			slots: make([]*connSlot, p.cfg.Size),
			conns: make([]atomic.Pointer[MuxConn], p.cfg.Size),
		}
		created.addr.Store(addr)
		for i := range created.slots {
			created.slots[i] = &connSlot{mirror: &created.conns[i]}
		}
		actual, _ := p.nodes.LoadOrStore(nodeID, created)
		set := actual.(*nodeConnSet)
		if !set.evicted.Load() {
			return set, nil
		}
		p.nodes.CompareAndDelete(nodeID, set)
	}
}

func (s *connSlot) clearClosedConn(mc *MuxConn) {
	s.mu.Lock()
	if s.conn.Load() == mc {
		s.conn.Store(nil)
		if s.mirror != nil {
			s.mirror.Store(nil)
		}
	}
	s.mu.Unlock()
}

func (s *nodeConnSet) close() {
	if s == nil {
		return
	}
	for i := range s.slots {
		if mc := s.slots[i].conn.Load(); mc != nil {
			mc.Close()
		}
	}
}

func (s *nodeConnSet) closeAndClear() {
	if s == nil {
		return
	}
	for i := range s.slots {
		s.slots[i].closeAndClear()
	}
}

func (s *nodeConnSet) evictAndClose() {
	if s == nil {
		return
	}
	s.evicted.Store(true)
	s.closeAndClear()
}

func (s *connSlot) closeAndClear() {
	s.mu.Lock()
	mc := s.conn.Load()
	s.conn.Store(nil)
	if s.mirror != nil {
		s.mirror.Store(nil)
	}
	s.lastErr = nil
	s.lastDialFail = time.Time{}
	s.mu.Unlock()
	if mc != nil {
		mc.Close()
	}
}

func (s *connSlot) finishDialIfActive(mc *MuxConn, ready chan struct{}, active func() bool) bool {
	s.mu.Lock()
	ok := active == nil || active()
	if ok {
		s.conn.Store(mc)
		if s.mirror != nil {
			s.mirror.Store(mc)
		}
		s.lastErr = nil
		s.lastDialFail = time.Time{}
	} else {
		s.lastErr = nil
		s.lastDialFail = time.Time{}
	}
	if s.ready == ready {
		s.ready = nil
	}
	s.mu.Unlock()
	close(ready)
	if !ok && mc != nil {
		mc.Close()
	}
	return ok
}

func (s *connSlot) waiterOrCooldown() (chan struct{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready != nil {
		return s.ready, nil
	}
	if s.lastErr != nil && time.Since(s.lastDialFail) < poolDialCooldown {
		return nil, s.lastErr
	}
	return nil, nil
}

func (s *connSlot) finishDial(mc *MuxConn, dialErr error, ready chan struct{}) {
	s.mu.Lock()
	if dialErr != nil {
		s.lastDialFail = time.Now()
		s.lastErr = dialErr
	} else {
		s.conn.Store(mc)
		if s.mirror != nil {
			s.mirror.Store(mc)
		}
		s.lastErr = nil
		s.lastDialFail = time.Time{}
	}
	if s.ready == ready {
		s.ready = nil
	}
	s.mu.Unlock()
	close(ready)
}

func setTCPKeepAlive(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(tcpKeepAlivePeriod)
	}
}
