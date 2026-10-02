//go:build e2e

package suite

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TCPLinkObservation records relay activity without decoding cluster frames.
type TCPLinkObservation struct {
	Forwarded int `json:"forwarded"`
	Closed    int `json:"closed"`
	Refused   int `json:"refused"`
}

type tcpPartitionBridge struct {
	from, to        uint64
	client, backend net.Conn
}

// ClusterTCPPartition owns bounded transparent relays for exact test processes.
// Static membership uses relay endpoints; public product listeners stay direct.
type ClusterTCPPartition struct {
	// mu serializes classification, link admission, cuts and shutdown ownership.
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	// wg joins acceptors and bridges, including their paired copy workers.
	wg   sync.WaitGroup
	lsof string
	// pids contains only exact product processes registered after owned Start.
	pids map[int]uint64
	// blocked cuts inter-node traffic; a node's own TCP is exempt.
	blocked map[uint64]bool
	// links retains counters for the finite configured process-pair inventory.
	links map[string]TCPLinkObservation
	// bridges admits at most 128 classifiers/dials/copy pairs, without a queue.
	bridges map[*tcpPartitionBridge]struct{}
	// listeners and closed retain idempotent lifecycle ownership through Close.
	listeners []net.Listener
	closed    bool
}

// tcpIdentityOutput bounds retained child output before allocation can grow.
type tcpIdentityOutput struct {
	data     []byte
	exceeded bool
}

func (b *tcpIdentityOutput) Write(data []byte) (int, error) {
	remaining := 4096 - len(b.data)
	n := min(len(data), remaining)
	b.data = append(b.data, data[:n]...)
	b.exceeded = b.exceeded || n != len(data)
	return len(data), nil
}

// NewClusterTCPPartition requires socket-to-process evidence from lsof rather
// than inferring sender identity from mutable product state or RPC payloads.
func NewClusterTCPPartition(t testing.TB) *ClusterTCPPartition {
	t.Helper()
	path, err := exec.LookPath("lsof")
	if err != nil {
		t.Fatalf("cluster TCP partition requires lsof: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &ClusterTCPPartition{ctx: ctx, cancel: cancel, lsof: path, pids: map[int]uint64{}, blocked: map[uint64]bool{}, links: map[string]TCPLinkObservation{}, bridges: map[*tcpPartitionBridge]struct{}{}}
	t.Cleanup(func() { p.Close() })
	return p
}

// WithClusterTCPPartition transparently relays every static cluster TCP link.
func WithClusterTCPPartition(p *ClusterTCPPartition) Option {
	return optionFunc(func(o *suiteOptions) { o.tcpPartition = p })
}

func (p *ClusterTCPPartition) start(t testing.TB, specs []NodeSpec) []NodeSpec {
	t.Helper()
	peers := append([]NodeSpec(nil), specs...)
	for i, spec := range specs {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("start cluster TCP relay: %v", err)
		}
		p.listeners = append(p.listeners, listener)
		peers[i].ClusterAddr = listener.Addr().String()
		p.wg.Add(1)
		go p.accept(listener, spec.ID, spec.ClusterAddr)
	}
	return peers
}

func (p *ClusterTCPPartition) register(node uint64, process *NodeProcess) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pids[process.Cmd.Process.Pid] = node
}

func (p *ClusterTCPPartition) accept(listener net.Listener, node uint64, target string) {
	defer p.wg.Done()
	for {
		client, err := listener.Accept()
		if err != nil {
			return
		}
		b := &tcpPartitionBridge{to: node, client: client}
		p.mu.Lock()
		// No waiting queue or unbounded dial/classification goroutines.
		if p.closed || len(p.bridges) >= 128 {
			p.mu.Unlock()
			_ = client.Close()
			continue
		}
		p.bridges[b] = struct{}{}
		p.wg.Add(1)
		p.mu.Unlock()
		go p.forward(b, target)
	}
}

func (p *ClusterTCPPartition) forward(b *tcpPartitionBridge, target string) {
	defer p.wg.Done()
	defer func() {
		p.mu.Lock()
		_ = b.client.Close()
		if b.backend != nil {
			_ = b.backend.Close()
		}
		delete(p.bridges, b)
		p.mu.Unlock()
	}()
	from, err := p.sourceNode(b.client)
	p.mu.Lock()
	if err != nil {
		p.mu.Unlock()
		return
	}
	b.from = from
	key := fmt.Sprintf("%d->%d", b.from, b.to)
	if p.closed || b.from != b.to && (p.blocked[b.from] || p.blocked[b.to]) {
		v := p.links[key]
		v.Refused++
		p.links[key] = v
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	backend, err := (&net.Dialer{Timeout: time.Second}).DialContext(p.ctx, "tcp", target)
	if err != nil {
		return
	}
	p.mu.Lock()
	b.backend = backend
	if p.closed || b.from != b.to && (p.blocked[b.from] || p.blocked[b.to]) {
		v := p.links[key]
		v.Refused++
		p.links[key] = v
		p.mu.Unlock()
		return
	}
	v := p.links[key]
	v.Forwarded++
	p.links[key] = v
	p.mu.Unlock()
	// Closing either copy closes both endpoints and joins the second worker.
	joined := make(chan struct{})
	go func() { _, _ = io.Copy(backend, b.client); _ = backend.Close(); _ = b.client.Close(); close(joined) }()
	_, _ = io.Copy(b.client, backend)
	_ = backend.Close()
	_ = b.client.Close()
	<-joined
}

func (p *ClusterTCPPartition) sourceNode(client net.Conn) (uint64, error) {
	_, port, err := net.SplitHostPort(client.RemoteAddr().String())
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(p.ctx, 3*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		call, done := context.WithTimeout(ctx, time.Second)
		output := &tcpIdentityOutput{data: make([]byte, 0, 4096)}
		cmd := exec.CommandContext(call, p.lsof, "-nP", "-a", "-iTCP:"+port, "-sTCP:ESTABLISHED", "-Fpn")
		cmd.Stdout = output
		_ = cmd.Run()
		done()
		if output.exceeded {
			return 0, fmt.Errorf("socket identity output exceeded bound")
		}
		p.mu.Lock()
		var found uint64
		var pid int
		want := "n" + client.RemoteAddr().String() + "->" + client.LocalAddr().String()
		for _, line := range strings.Split(string(output.data), "\n") {
			if strings.HasPrefix(line, "p") {
				pid, _ = strconv.Atoi(strings.TrimPrefix(line, "p"))
				continue
			}
			if line != want {
				continue
			}
			if id := p.pids[pid]; id != 0 {
				if found != 0 && found != id {
					p.mu.Unlock()
					return 0, fmt.Errorf("ambiguous socket process identity")
				}
				found = id
			}
		}
		p.mu.Unlock()
		if found != 0 {
			return found, nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	return 0, ctx.Err()
}

// Isolate atomically refuses reconnects and closes all existing directed links
// involving node; the other two nodes' links and all public ports remain live.
func (p *ClusterTCPPartition) Isolate(node uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	known := false
	for _, id := range p.pids {
		known = known || id == node
	}
	if p.closed || !known {
		return fmt.Errorf("unknown or closed partition node %d", node)
	}
	p.blocked[node] = true
	for b := range p.bridges {
		if b.from == 0 || b.from == b.to || b.from != node && b.to != node {
			continue
		}
		if b.from != 0 {
			key := fmt.Sprintf("%d->%d", b.from, b.to)
			v := p.links[key]
			v.Closed++
			p.links[key] = v
		}
		_ = b.client.Close()
		if b.backend != nil {
			_ = b.backend.Close()
		}
	}
	return nil
}

// Heal reopens exactly these test-owned cluster links without process restart.
func (p *ClusterTCPPartition) Heal() {
	p.mu.Lock()
	defer p.mu.Unlock()
	clear(p.blocked)
}

// Snapshot returns immutable bounded per-direction relay counters.
func (p *ClusterTCPPartition) Snapshot() map[string]TCPLinkObservation {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]TCPLinkObservation, len(p.links))
	for k, v := range p.links {
		out[k] = v
	}
	return out
}

// Close cancels classifiers/dials, closes sockets/listeners and joins workers.
func (p *ClusterTCPPartition) Close() {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		p.cancel()
		for _, listener := range p.listeners {
			_ = listener.Close()
		}
		for b := range p.bridges {
			_ = b.client.Close()
			if b.backend != nil {
				_ = b.backend.Close()
			}
		}
	}
	p.mu.Unlock()
	p.wg.Wait()
}
