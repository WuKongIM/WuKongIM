package server

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/client"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

// pairSeq provides globally unique IDs so sub-benchmarks never collide on UIDs.
var pairSeq atomic.Int64

// clientPair holds a connected sender/receiver pair and the channel between them.
type clientPair struct {
	sender   *client.Client
	receiver *client.Client
	channel  *client.Channel
}

// newClientPair creates a connected sender→receiver pair.
// It registers cleanup via b.Cleanup to ensure resources are released.
func newClientPair(b *testing.B, addr string) *clientPair {
	b.Helper()
	id := pairSeq.Add(1)
	senderUID := fmt.Sprintf("bs%d", id)
	receiverUID := fmt.Sprintf("br%d", id)

	sender := client.New(addr, client.WithUID(senderUID))
	if err := sender.Connect(); err != nil {
		b.Fatalf("sender connect: %v", err)
	}

	receiver := client.New(addr, client.WithUID(receiverUID))
	if err := receiver.Connect(); err != nil {
		sender.Close()
		b.Fatalf("receiver connect: %v", err)
	}

	ch := client.NewChannel(receiverUID, wkproto.ChannelTypePerson)

	b.Cleanup(func() {
		sender.Close()
		receiver.Close()
	})

	return &clientPair{sender: sender, receiver: receiver, channel: ch}
}

// benchFreePort returns an available TCP port by briefly binding to :0.
func benchFreePort(b *testing.B) int {
	b.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("get free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// freePort is like benchFreePort but for use outside benchmarks.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// BenchmarkMessageE2E is the top-level benchmark that shares a single test server
// across all Latency and Throughput sub-benchmarks.
func BenchmarkMessageE2E(b *testing.B) {
	httpPort := benchFreePort(b)
	tcpPort := benchFreePort(b)
	wsPort := benchFreePort(b)
	clusterPort := benchFreePort(b)

	tcpAddr := fmt.Sprintf("tcp://127.0.0.1:%d", tcpPort)
	clusterAddr := fmt.Sprintf("tcp://127.0.0.1:%d", clusterPort)

	s := NewTestServer(b,
		options.WithMode(options.TestMode),
		options.WithDemoOn(false),
		options.WithManagerOn(false),
		options.WithHTTPAddr(fmt.Sprintf("127.0.0.1:%d", httpPort)),
		options.WithAddr(tcpAddr),
		options.WithWSAddr(fmt.Sprintf("ws://127.0.0.1:%d", wsPort)),
		options.WithClusterAddr(clusterAddr),
		options.WithClusterServerAddr(clusterAddr),
		options.WithClusterAPIURL(fmt.Sprintf("http://127.0.0.1:%d", httpPort)),
	)
	if err := s.Start(); err != nil {
		b.Fatalf("server start: %v", err)
	}
	b.Cleanup(func() { s.StopNoErr() })

	s.MustWaitAllSlotsReady(time.Second * 10)
	// Allow cluster slot raft groups to fully initialize after leadership is established.
	time.Sleep(time.Second * 2)

	addr := tcpAddr

	b.Run("Latency", func(b *testing.B) {
		for _, size := range []int{64, 256, 1024, 4096} {
			b.Run(formatSize(size), func(b *testing.B) {
				benchLatency(b, addr, size)
			})
		}
	})

	b.Run("Throughput", func(b *testing.B) {
		for _, np := range []int{1, 10, 100} {
			b.Run(fmt.Sprintf("Pairs_%d", np), func(b *testing.B) {
				benchThroughput(b, addr, np)
			})
		}
	})
}

// benchLatency measures single-message end-to-end latency.
// Each iteration: sender.SendMessage → Flush → wait for receiver callback.
func benchLatency(b *testing.B, addr string, payloadSize int) {
	pair := newClientPair(b, addr)

	recvCh := make(chan struct{}, 1)
	pair.receiver.SetOnRecv(func(recv *wkproto.RecvPacket) error {
		select {
		case recvCh <- struct{}{}:
		default:
		}
		return nil
	})

	payload := bytes.Repeat([]byte("x"), payloadSize)

	// Warmup: send 5 messages to initialize channel/tag and fill caches.
	for range 5 {
		if err := pair.sender.SendMessage(pair.channel, payload); err != nil {
			b.Fatalf("warmup send: %v", err)
		}
		if err := pair.sender.Flush(); err != nil {
			b.Fatalf("warmup flush: %v", err)
		}
		select {
		case <-recvCh:
		case <-time.After(10 * time.Second):
			b.Fatal("warmup recv timeout")
		}
	}

	b.SetBytes(int64(payloadSize))
	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		if err := pair.sender.SendMessage(pair.channel, payload); err != nil {
			b.Fatalf("send: %v", err)
		}
		if err := pair.sender.Flush(); err != nil {
			b.Fatalf("flush: %v", err)
		}
		select {
		case <-recvCh:
		case <-time.After(10 * time.Second):
			b.Fatal("recv timeout")
		}
	}
}

// benchThroughput measures aggregate throughput with multiple concurrent sender/receiver pairs.
// Each goroutine owns one pair — no lock contention between goroutines.
func benchThroughput(b *testing.B, addr string, numPairs int) {
	const payloadSize = 256
	payload := bytes.Repeat([]byte("x"), payloadSize)

	pairs := make([]*clientPair, numPairs)
	for i := range numPairs {
		pairs[i] = newClientPair(b, addr)
	}

	var received atomic.Int64

	// Set up receive callbacks.
	for i := range numPairs {
		pairs[i].receiver.SetOnRecv(func(recv *wkproto.RecvPacket) error {
			received.Add(1)
			return nil
		})
	}

	// Warmup: each pair sends 1 message and waits for delivery.
	var warmupWg sync.WaitGroup
	for i := range numPairs {
		warmupWg.Add(1)
		go func(p *clientPair) {
			defer warmupWg.Done()
			if err := p.sender.SendMessage(p.channel, payload); err != nil {
				return
			}
			p.sender.Flush()
		}(pairs[i])
	}
	warmupWg.Wait()
	// Wait for all warmup messages to arrive.
	deadline := time.After(30 * time.Second)
	for received.Load() < int64(numPairs) {
		select {
		case <-deadline:
			b.Fatalf("warmup recv timeout: got %d/%d", received.Load(), numPairs)
		default:
			time.Sleep(time.Millisecond)
		}
	}

	// Reset counter and timer for the measured run.
	received.Store(0)
	b.SetBytes(int64(payloadSize))
	b.ResetTimer()
	b.ReportAllocs()

	totalMessages := int64(b.N)
	msgsPerPair := totalMessages / int64(numPairs)
	// Give remainder to the last pair so total == b.N.
	remainder := totalMessages - msgsPerPair*int64(numPairs)

	var sendWg sync.WaitGroup
	for i := range numPairs {
		sendWg.Add(1)
		count := msgsPerPair
		if i == numPairs-1 {
			count += remainder
		}
		go func(p *clientPair, n int64) {
			defer sendWg.Done()
			for range n {
				if err := p.sender.SendMessage(p.channel, payload); err != nil {
					return
				}
			}
			p.sender.Flush()
		}(pairs[i], count)
	}
	sendWg.Wait()

	// Wait until all messages are received.
	timeout := time.After(60 * time.Second)
	for received.Load() < totalMessages {
		select {
		case <-timeout:
			b.Fatalf("recv timeout: got %d/%d", received.Load(), totalMessages)
		default:
			time.Sleep(time.Millisecond)
		}
	}

	b.StopTimer()
}

// formatSize returns a human-readable size label.
func formatSize(n int) string {
	switch {
	case n >= 1024:
		return fmt.Sprintf("%dKB", n/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// ---------------------------------------------------------------------------
// Cross-node (cluster) E2E benchmark
//
// WuKongIM uses process-global singletons (options.G, service.Store,
// service.Cluster, …) so two Server instances cannot coexist in one process.
// We work around this by running node 2 as a **subprocess** of the test binary.
// ---------------------------------------------------------------------------

// clusterEnv holds the env-var keys used to pass port info to the subprocess.
const (
	envNode2       = "WK_BENCH_NODE2"
	envHTTPPort    = "WK_HTTP_PORT"
	envTCPPort     = "WK_TCP_PORT"
	envWSPort      = "WK_WS_PORT"
	envClusterPort = "WK_CLUSTER_PORT"
	envManagerPort = "WK_MANAGER_PORT"
	envInitNode1   = "WK_INIT_NODE1" // "id:addr"
	envInitNode2   = "WK_INIT_NODE2"
)

// TestClusterBenchNode2 is invoked as a **subprocess** by BenchmarkClusterE2E.
// It starts a WuKongIM server, prints "READY" on stdout, then blocks until
// stdin is closed (parent exits or kills the subprocess).
func TestClusterBenchNode2(t *testing.T) {
	if os.Getenv(envNode2) != "1" {
		t.Skip("only runs as subprocess for cluster benchmark")
	}

	mustEnvInt := func(key string) int {
		v, err := strconv.Atoi(os.Getenv(key))
		if err != nil {
			t.Fatalf("bad env %s: %v", key, err)
		}
		return v
	}

	httpPort := mustEnvInt(envHTTPPort)
	tcpPort := mustEnvInt(envTCPPort)
	wsPort := mustEnvInt(envWSPort)
	clusterPort := mustEnvInt(envClusterPort)
	managerPort := mustEnvInt(envManagerPort)

	parseNode := func(key string) *options.Node {
		s := os.Getenv(key)
		var id uint64
		var addr string
		fmt.Sscanf(s, "%d:%s", &id, &addr)
		return &options.Node{Id: id, ServerAddr: addr}
	}
	nodes := []*options.Node{parseNode(envInitNode1), parseNode(envInitNode2)}

	tcpAddr := fmt.Sprintf("tcp://127.0.0.1:%d", tcpPort)

	s := NewTestServer(t,
		options.WithDemoOn(false),
		options.WithManagerAddr(fmt.Sprintf("127.0.0.1:%d", managerPort)),
		options.WithHTTPAddr(fmt.Sprintf("127.0.0.1:%d", httpPort)),
		options.WithAddr(tcpAddr),
		options.WithWSAddr(fmt.Sprintf("ws://127.0.0.1:%d", wsPort)),
		options.WithClusterAddr(fmt.Sprintf("tcp://127.0.0.1:%d", clusterPort)),
		options.WithClusterAPIURL(fmt.Sprintf("http://127.0.0.1:%d", httpPort)),
		options.WithClusterNodeId(1002),
		options.WithClusterInitNodes(nodes),
	)
	if err := s.Start(); err != nil {
		t.Fatalf("node2 start: %v", err)
	}
	defer s.StopNoErr()

	// Signal readiness to the parent.
	fmt.Println("READY")

	// Block until parent closes our stdin (or kills us).
	buf := make([]byte, 1)
	os.Stdin.Read(buf)
}

// clusterPair holds references to the two-node cluster.
type clusterPair struct {
	s1       *Server      // in-process node
	node2cmd *exec.Cmd    // subprocess node
	addr1    string       // s1 TCP address
	addr2    string       // s2 TCP address (subprocess)
}

// newBenchCluster creates a 2-node cluster: node 1 in-process, node 2 as subprocess.
func newBenchCluster(b *testing.B) *clusterPair {
	b.Helper()

	// Allocate 5 free ports per node (http, tcp, ws, cluster, manager).
	mustFree := func() int {
		p, err := freePort()
		if err != nil {
			b.Fatalf("free port: %v", err)
		}
		return p
	}

	httpPort1, tcpPort1, wsPort1, clusterPort1, managerPort1 :=
		mustFree(), mustFree(), mustFree(), mustFree(), mustFree()
	httpPort2, tcpPort2, wsPort2, clusterPort2, managerPort2 :=
		mustFree(), mustFree(), mustFree(), mustFree(), mustFree()

	tcpAddr1 := fmt.Sprintf("tcp://127.0.0.1:%d", tcpPort1)
	tcpAddr2 := fmt.Sprintf("tcp://127.0.0.1:%d", tcpPort2)
	clusterAddr1 := fmt.Sprintf("tcp://127.0.0.1:%d", clusterPort1)

	node1Str := fmt.Sprintf("1001:127.0.0.1:%d", clusterPort1)
	node2Str := fmt.Sprintf("1002:127.0.0.1:%d", clusterPort2)

	nodes := []*options.Node{
		{Id: 1001, ServerAddr: fmt.Sprintf("127.0.0.1:%d", clusterPort1)},
		{Id: 1002, ServerAddr: fmt.Sprintf("127.0.0.1:%d", clusterPort2)},
	}

	// --- Start node 2 as subprocess first (so it's running when node 1 tries to connect). ---
	cmd := exec.Command(os.Args[0], "-test.run=^TestClusterBenchNode2$", "-test.v", "-test.timeout=30m")
	cmd.Env = append(os.Environ(),
		envNode2+"=1",
		fmt.Sprintf("%s=%d", envHTTPPort, httpPort2),
		fmt.Sprintf("%s=%d", envTCPPort, tcpPort2),
		fmt.Sprintf("%s=%d", envWSPort, wsPort2),
		fmt.Sprintf("%s=%d", envClusterPort, clusterPort2),
		fmt.Sprintf("%s=%d", envManagerPort, managerPort2),
		envInitNode1+"="+node1Str,
		envInitNode2+"="+node2Str,
	)
	cmd.Stderr = os.Stderr // forward logs
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		b.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		b.Fatalf("start node2 subprocess: %v", err)
	}

	// Wait for the subprocess to print "READY".
	readyCh := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "READY" {
				close(readyCh)
				return
			}
		}
	}()
	select {
	case <-readyCh:
	case <-time.After(60 * time.Second):
		cmd.Process.Kill()
		b.Fatal("node2 subprocess did not become ready within 60s")
	}

	// --- Start node 1 in-process. ---
	s1 := NewTestServer(b,
		options.WithDemoOn(false),
		options.WithManagerAddr(fmt.Sprintf("127.0.0.1:%d", managerPort1)),
		options.WithHTTPAddr(fmt.Sprintf("127.0.0.1:%d", httpPort1)),
		options.WithAddr(tcpAddr1),
		options.WithWSAddr(fmt.Sprintf("ws://127.0.0.1:%d", wsPort1)),
		options.WithClusterAddr(clusterAddr1),
		options.WithClusterAPIURL(fmt.Sprintf("http://127.0.0.1:%d", httpPort1)),
		options.WithClusterNodeId(1001),
		options.WithClusterInitNodes(nodes),
		func(opts *options.Options) {
			opts.Plugin.SocketPath = fmt.Sprintf("%s/wk.sock", b.TempDir())
		},
	)
	if err := s1.Start(); err != nil {
		cmd.Process.Kill()
		b.Fatalf("s1 start: %v", err)
	}

	// Wait for cluster to stabilize.
	s1.MustWaitAllSlotsReady(time.Second * 30)
	time.Sleep(2 * time.Second)

	b.Cleanup(func() {
		s1.StopNoErr()
		cmd.Process.Kill()
		cmd.Wait()
	})

	return &clusterPair{
		s1: s1, node2cmd: cmd,
		addr1: tcpAddr1, addr2: tcpAddr2,
	}
}

// connectWithRetry connects a client, retrying until the cluster slots are ready.
func connectWithRetry(b *testing.B, addr, uid string) *client.Client {
	b.Helper()
	const maxRetries = 15
	var lastErr error
	for i := range maxRetries {
		cli := client.New(addr, client.WithUID(uid))
		if err := cli.Connect(); err == nil {
			return cli
		} else {
			lastErr = err
			cli.Close()
		}
		if i < maxRetries-1 {
			time.Sleep(time.Second)
		}
	}
	b.Fatalf("connect %s to %s failed after %d retries: %v", uid, addr, maxRetries, lastErr)
	return nil
}

// newCrossNodeClientPair creates a sender/receiver pair where sender connects to
// one node and receiver connects to a different node, forcing cross-cluster delivery.
func newCrossNodeClientPair(b *testing.B, senderAddr, receiverAddr string) *clientPair {
	b.Helper()
	id := pairSeq.Add(1)
	senderUID := fmt.Sprintf("cs%d", id)
	receiverUID := fmt.Sprintf("cr%d", id)

	sender := connectWithRetry(b, senderAddr, senderUID)
	receiver := connectWithRetry(b, receiverAddr, receiverUID)

	ch := client.NewChannel(receiverUID, wkproto.ChannelTypePerson)

	b.Cleanup(func() {
		sender.Close()
		receiver.Close()
	})

	return &clientPair{sender: sender, receiver: receiver, channel: ch}
}

// BenchmarkClusterE2E measures cross-node message delivery performance.
// Messages travel: sender → Node1 → [Raft Propose] → [Cluster Transport] → Node2 → receiver.
func BenchmarkClusterE2E(b *testing.B) {
	cluster := newBenchCluster(b)

	b.Run("Latency", func(b *testing.B) {
		for _, size := range []int{64, 256, 1024, 4096} {
			b.Run(formatSize(size), func(b *testing.B) {
				benchClusterLatency(b, cluster, size)
			})
		}
	})

	b.Run("Throughput", func(b *testing.B) {
		for _, np := range []int{1, 10, 50} {
			b.Run(fmt.Sprintf("Pairs_%d", np), func(b *testing.B) {
				benchClusterThroughput(b, cluster, np)
			})
		}
	})
}

// benchClusterLatency measures single-message cross-node latency.
func benchClusterLatency(b *testing.B, cluster *clusterPair, payloadSize int) {
	pair := newCrossNodeClientPair(b, cluster.addr1, cluster.addr2)

	recvCh := make(chan struct{}, 1)
	pair.receiver.SetOnRecv(func(recv *wkproto.RecvPacket) error {
		select {
		case recvCh <- struct{}{}:
		default:
		}
		return nil
	})

	payload := bytes.Repeat([]byte("x"), payloadSize)

	// Warmup: send 5 messages to initialize channel/tag and fill caches.
	for range 5 {
		if err := pair.sender.SendMessage(pair.channel, payload); err != nil {
			b.Fatalf("warmup send: %v", err)
		}
		if err := pair.sender.Flush(); err != nil {
			b.Fatalf("warmup flush: %v", err)
		}
		select {
		case <-recvCh:
		case <-time.After(10 * time.Second):
			b.Fatal("warmup recv timeout")
		}
	}

	b.SetBytes(int64(payloadSize))
	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		if err := pair.sender.SendMessage(pair.channel, payload); err != nil {
			b.Fatalf("send: %v", err)
		}
		if err := pair.sender.Flush(); err != nil {
			b.Fatalf("flush: %v", err)
		}
		select {
		case <-recvCh:
		case <-time.After(10 * time.Second):
			b.Fatal("recv timeout")
		}
	}
}

// benchClusterThroughput measures aggregate cross-node throughput with concurrent pairs.
func benchClusterThroughput(b *testing.B, cluster *clusterPair, numPairs int) {
	const payloadSize = 256
	payload := bytes.Repeat([]byte("x"), payloadSize)

	pairs := make([]*clientPair, numPairs)
	for i := range numPairs {
		pairs[i] = newCrossNodeClientPair(b, cluster.addr1, cluster.addr2)
	}

	var received atomic.Int64

	// Set up receive callbacks.
	for i := range numPairs {
		pairs[i].receiver.SetOnRecv(func(recv *wkproto.RecvPacket) error {
			received.Add(1)
			return nil
		})
	}

	// Warmup: each pair sends 1 message and waits for delivery.
	var warmupWg sync.WaitGroup
	for i := range numPairs {
		warmupWg.Add(1)
		go func(p *clientPair) {
			defer warmupWg.Done()
			if err := p.sender.SendMessage(p.channel, payload); err != nil {
				return
			}
			p.sender.Flush()
		}(pairs[i])
	}
	warmupWg.Wait()
	// Wait for all warmup messages to arrive.
	deadline := time.After(30 * time.Second)
	for received.Load() < int64(numPairs) {
		select {
		case <-deadline:
			b.Fatalf("warmup recv timeout: got %d/%d", received.Load(), numPairs)
		default:
			time.Sleep(time.Millisecond)
		}
	}

	// Reset counter and timer for the measured run.
	received.Store(0)
	b.SetBytes(int64(payloadSize))
	b.ResetTimer()
	b.ReportAllocs()

	totalMessages := int64(b.N)
	msgsPerPair := totalMessages / int64(numPairs)
	remainder := totalMessages - msgsPerPair*int64(numPairs)

	var sendWg sync.WaitGroup
	for i := range numPairs {
		sendWg.Add(1)
		count := msgsPerPair
		if i == numPairs-1 {
			count += remainder
		}
		go func(p *clientPair, n int64) {
			defer sendWg.Done()
			for range n {
				if err := p.sender.SendMessage(p.channel, payload); err != nil {
					return
				}
			}
			p.sender.Flush()
		}(pairs[i], count)
	}
	sendWg.Wait()

	// Wait until all messages are received.
	timeout := time.After(60 * time.Second)
	for received.Load() < totalMessages {
		select {
		case <-timeout:
			b.Fatalf("recv timeout: got %d/%d", received.Load(), totalMessages)
		default:
			time.Sleep(time.Millisecond)
		}
	}

	b.StopTimer()
}
