//go:build integration

package cluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelworker "github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	channelMetaPressureRate        = 1000
	channelMetaPressureWorkers     = 512
	channelMetaPressureHotChannels = 100
	channelMetaPressureColdEvery   = 7
)

func TestThreeNodeColdPersonChannelWaveMeetsAttemptBudget(t *testing.T) {
	const (
		total          = 4000
		rate           = 2000
		workersPerNode = 1000
	)
	workers := workersPerNode * 3
	observer := newChannelMetaPressureObserver()
	nodes := newChannelMetaPressureCluster(t, observer)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	waitChannelMetaPlacementReady(t, nodes)
	waitChannelMetaSlotsReady(t, nodes, 12)

	latencies := make([]time.Duration, total)
	jobs := make(chan int, total)
	var group sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	group.Add(workers)
	started := time.Now()
	for range workers {
		go func() {
			defer group.Done()
			for index := range jobs {
				err := appendColdPersonChannelPressureMessage(nodes, index)
				latencies[index] = time.Since(started.Add(time.Duration(index) * time.Second / rate))
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("cold person append index=%d: %w", index, err)
					}
					errMu.Unlock()
				}
			}
		}()
	}
	for index := 0; index < total; index++ {
		waitChannelMetaPressureUntil(started.Add(time.Duration(index) * time.Second / rate))
		jobs <- index
	}
	close(jobs)
	group.Wait()
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p99 := latencies[(len(latencies)*99-1)/100]
	maximum := latencies[len(latencies)-1]
	observer.mu.Lock()
	batchCount := len(observer.batchSize)
	batchItems := 0
	for _, items := range observer.batchSize {
		batchItems += items
	}
	stageP99 := make(map[string]time.Duration)
	for _, stage := range []string{"meta_create_write", "meta_create_propose", "runtime_append_wait", "store_append_wait"} {
		values := append([]time.Duration(nil), observer.stages[stage]...)
		if len(values) == 0 {
			continue
		}
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		stageP99[stage] = values[(len(values)*99-1)/100]
	}
	observer.mu.Unlock()
	averageBatch := 0.0
	if batchCount > 0 {
		averageBatch = float64(batchItems) / float64(batchCount)
	}
	t.Logf("cold person latency p99=%v max=%v metadata batches=%d average-items=%.2f stage-p99=%v", p99, maximum, batchCount, averageBatch, stageP99)
	if p99 > 2*time.Second || maximum > 5*time.Second {
		t.Fatalf("cold person latency p99=%v max=%v; want p99<=2s and max<=5s", p99, maximum)
	}
}

// BenchmarkThreeNodeChannelAppendWithSlotMetaPressure1000QPS keeps ordinary
// hot Channel appends at 1000 QPS while roughly one seventh of requests create a fresh
// Channel through the production Slot Raft metadata path. Nodes, transport,
// Slot storage, Channel storage, and quorum replication are all real; only the
// product entry protocol and delivery fanout are omitted.
func BenchmarkThreeNodeChannelAppendWithSlotMetaPressure1000QPS(b *testing.B) {
	benchmarkThreeNodeChannelAppendWithSlotMetaPressure1000QPS(b, channelMetaPressureBenchmarkOptions{
		coldEvery: channelMetaPressureColdEvery, payload: channelMetaPressurePayload,
	})
}

// BenchmarkThreeNodeChannelAppendWithSlotMetaPressureMatrix1000QPS identifies
// whether hot-tail interference follows payload bytes or cold-Channel cadence.
func BenchmarkThreeNodeChannelAppendWithSlotMetaPressureMatrix1000QPS(b *testing.B) {
	for _, tc := range []struct {
		name string
		opts channelMetaPressureBenchmarkOptions
	}{
		{name: "tiny-cold-every-7", opts: channelMetaPressureBenchmarkOptions{coldEvery: 7, payload: func(int) []byte { return make([]byte, 21) }}},
		{name: "fixed-256-cold-every-7", opts: channelMetaPressureBenchmarkOptions{coldEvery: 7, payload: func(int) []byte { return make([]byte, 256) }}},
		{name: "fixed-1024-cold-every-7", opts: channelMetaPressureBenchmarkOptions{coldEvery: 7, payload: func(int) []byte { return make([]byte, 1024) }}},
		{name: "formal-cold-every-100", opts: channelMetaPressureBenchmarkOptions{coldEvery: 100, payload: channelMetaPressurePayload}},
		{name: "formal-cold-every-20", opts: channelMetaPressureBenchmarkOptions{coldEvery: 20, payload: channelMetaPressurePayload}},
		{name: "formal-cold-every-7", opts: channelMetaPressureBenchmarkOptions{coldEvery: 7, payload: channelMetaPressurePayload}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkThreeNodeChannelAppendWithSlotMetaPressure1000QPS(b, tc.opts)
		})
	}
}

// BenchmarkThreeNodePersonDirectoryBatchMatrix1000QPS measures durable task
// admission on the real three-node Slot/Raft path at the cold-directory rate
// seen by the reviewed 1000 QPS workload. UID projection is intentionally not
// on the measured SEND admission path.
func BenchmarkThreeNodePersonDirectoryBatchMatrix1000QPS(b *testing.B) {
	observer := newChannelMetaPressureObserver()
	nodes := newChannelMetaPressureCluster(b, observer)
	startNodes(b, nodes...)
	b.Cleanup(func() { stopNodes(b, nodes...) })
	waitClusterReady(b, nodes...)
	waitChannelMetaPlacementReady(b, nodes)
	waitChannelMetaSlotsReady(b, nodes, 12)

	for _, tc := range []struct {
		batchItems int
		maxActive  int
	}{
		{batchItems: 4, maxActive: 8},
		{batchItems: 8, maxActive: 8},
		{batchItems: 12, maxActive: 8},
		{batchItems: 30, maxActive: 4},
		{batchItems: 60, maxActive: 2},
		{batchItems: 60, maxActive: 4},
		{batchItems: 60, maxActive: 8},
		{batchItems: 128, maxActive: 8},
	} {
		name := fmt.Sprintf("batch-%03d-active-%02d", tc.batchItems, tc.maxActive)
		b.Run(name, func(b *testing.B) {
			observer.reset()
			benchmarkThreeNodePersonDirectoryBatch1000QPS(b, nodes, name, tc.batchItems, tc.maxActive)
			observer.report(b)
		})
	}
}

// BenchmarkThreeNodePersonDirectoryBurst1800QPS reproduces the cold-person
// directory arrival rate observed in the failed 2000 QPS cloud rehearsal. It
// retains the failed production wave as a baseline and compares bounded batch
// and concurrency candidates so Slot proposal amplification stays visible.
func BenchmarkThreeNodePersonDirectoryBurst1800QPS(b *testing.B) {
	observer := newChannelMetaPressureObserver()
	nodes := newChannelMetaPressureCluster(b, observer)
	startNodes(b, nodes...)
	b.Cleanup(func() { stopNodes(b, nodes...) })
	waitClusterReady(b, nodes...)
	waitChannelMetaPlacementReady(b, nodes)
	waitChannelMetaSlotsReady(b, nodes, 12)

	for _, tc := range []struct {
		batchItems int
		maxActive  int
	}{
		{batchItems: 8, maxActive: 8},
		{batchItems: 16, maxActive: 8},
		{batchItems: 32, maxActive: 8},
		{batchItems: 32, maxActive: 12},
		{batchItems: 32, maxActive: 16},
		{batchItems: 64, maxActive: 8},
		{batchItems: 32, maxActive: 4},
	} {
		name := fmt.Sprintf("batch-%03d-active-%02d", tc.batchItems, tc.maxActive)
		b.Run(name, func(b *testing.B) {
			observer.reset()
			benchmarkThreeNodePersonDirectoryBatchAtRate(b, nodes, name, tc.batchItems, tc.maxActive, 1800)
			observer.report(b)
		})
	}
}

// BenchmarkThreeNodeHotAppendWithPersonDirectoryPressure1000QPS measures the
// reviewed hot append rate while the same cluster concurrently establishes
// person directories at the observed 720 items/second cold-wave rate.
func BenchmarkThreeNodeHotAppendWithPersonDirectoryPressure1000QPS(b *testing.B) {
	for _, tc := range []struct {
		name       string
		batchItems int
		maxActive  int
	}{
		{name: "admit-batch-004-active-08", batchItems: 4, maxActive: 8},
		{name: "admit-batch-008-active-08", batchItems: 8, maxActive: 8},
		{name: "admit-batch-012-active-08", batchItems: 12, maxActive: 8},
		{name: "admit-batch-060-active-04", batchItems: 60, maxActive: 4},
		{name: "admit-batch-060-active-08", batchItems: 60, maxActive: 8},
		{name: "admit-batch-120-active-08", batchItems: 120, maxActive: 8},
		{name: "admit-batch-128-active-08", batchItems: 128, maxActive: 8},
	} {
		name := tc.name
		b.Run(name, func(b *testing.B) {
			observer := newChannelMetaPressureObserver()
			nodes := newChannelMetaPressureCluster(b, observer)
			startNodes(b, nodes...)
			b.Cleanup(func() { stopNodes(b, nodes...) })
			waitClusterReady(b, nodes...)
			waitChannelMetaPlacementReady(b, nodes)
			waitChannelMetaSlotsReady(b, nodes, 12)
			benchmarkHotAppendWithPersonDirectoryPressure(b, nodes, name, tc.batchItems, tc.maxActive)
			observer.report(b)
		})
	}
}

const personDirectoryPressureRate = 720

type personDirectoryPressureBatch struct {
	nodeIndex int
	indices   []int
	sealAt    time.Duration
}

func benchmarkThreeNodePersonDirectoryBatch1000QPS(b *testing.B, nodes []*Node, namespace string, batchItems, maxActive int) {
	benchmarkThreeNodePersonDirectoryBatchAtRate(b, nodes, namespace, batchItems, maxActive, personDirectoryPressureRate)
}

func benchmarkThreeNodePersonDirectoryBatchAtRate(b *testing.B, nodes []*Node, namespace string, batchItems, maxActive, rate int) {
	if batchItems <= 0 || maxActive <= 0 || rate <= 0 || len(nodes) == 0 {
		b.Fatal("person-directory benchmark requires nodes and positive batch/concurrency limits")
	}
	streams := make([][]int, len(nodes))
	for index := 0; index < b.N; index++ {
		nodeIndex := index % len(nodes)
		streams[nodeIndex] = append(streams[nodeIndex], index)
	}
	batches := make([]personDirectoryPressureBatch, 0, (b.N+batchItems-1)/batchItems)
	for nodeIndex, stream := range streams {
		for start := 0; start < len(stream); start += batchItems {
			end := min(start+batchItems, len(stream))
			indices := stream[start:end]
			last := indices[len(indices)-1]
			batches = append(batches, personDirectoryPressureBatch{
				nodeIndex: nodeIndex,
				indices:   indices,
				sealAt:    time.Duration(last) * time.Second / time.Duration(rate),
			})
		}
	}
	sort.Slice(batches, func(i, j int) bool { return batches[i].sealAt < batches[j].sealAt })

	latencies := make([]time.Duration, b.N)
	execution := make([]time.Duration, len(batches))
	admissionLatency := make([]time.Duration, len(batches))
	jobs := make(chan int, 24)
	semaphores := make([]chan struct{}, len(nodes))
	for index := range semaphores {
		semaphores[index] = make(chan struct{}, maxActive)
	}
	var workers sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	var benchmarkStarted time.Time
	workers.Add(24)
	for range 24 {
		go func() {
			defer workers.Done()
			for batchIndex := range jobs {
				batch := batches[batchIndex]
				semaphore := semaphores[batch.nodeIndex]
				semaphore <- struct{}{}
				physical, err := admitPersonDirectoryPressureBatch(nodes[batch.nodeIndex], namespace, batch.indices)
				<-semaphore
				execution[batchIndex] = physical
				admissionLatency[batchIndex] = time.Since(benchmarkStarted.Add(batch.sealAt))
				last := batch.indices[len(batch.indices)-1]
				for _, index := range batch.indices {
					latencies[index] = admissionLatency[batchIndex] + time.Duration(last-index)*time.Second/time.Duration(rate)
				}
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("person-directory batch node=%d first=%d items=%d: %w", batch.nodeIndex+1, batch.indices[0], len(batch.indices), err)
					}
					errMu.Unlock()
				}
			}
		}()
	}

	b.ReportAllocs()
	b.ResetTimer()
	benchmarkStarted = time.Now()
	for batchIndex, batch := range batches {
		waitChannelMetaPressureUntil(benchmarkStarted.Add(batch.sealAt))
		jobs <- batchIndex
	}
	close(jobs)
	workers.Wait()
	b.StopTimer()
	if firstErr != nil {
		b.Fatal(firstErr)
	}
	reportChannelMetaPressureLatency(b, "person-directory", latencies)
	reportChannelMetaPressureLatency(b, "person-directory-execution", execution)
	reportChannelMetaPressureLatency(b, "person-directory-admission", admissionLatency)
	b.ReportMetric(float64(len(batches)), "person-directory-batches")
}

func admitPersonDirectoryPressureBatch(node *Node, namespace string, indices []int) (time.Duration, error) {
	tasks := make([]metadb.PersonDirectoryTask, 0, len(indices))
	for _, index := range indices {
		left := fmt.Sprintf("person-%s-left-%09d", namespace, index)
		right := fmt.Sprintf("person-%s-right-%09d", namespace, index)
		channelID := runtimechannelid.EncodePersonChannel(left, right)
		tasks = append(tasks, metadb.PersonDirectoryTask{ChannelID: channelID, ChannelType: 1, CreatedAt: 1})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	started := time.Now()
	results := node.AdmitPersonDirectoryTasks(ctx, tasks)
	for _, err := range results {
		if err != nil {
			return time.Since(started), err
		}
	}
	return time.Since(started), nil
}

func benchmarkHotAppendWithPersonDirectoryPressure(b *testing.B, nodes []*Node, namespace string, batchItems, maxActive int) {
	const messageIDBase = uint64(0)
	for index := 0; index < channelMetaPressureHotChannels; index++ {
		if err := appendChannelMetaPressureMessage(
			nodes, index, fmt.Sprintf("person-pressure-%s-hot-%03d", namespace, index),
			messageIDBase+uint64(index+1), channelMetaPressurePayload,
		); err != nil {
			b.Fatalf("warm hot channel %d: %v", index, err)
		}
	}
	personItems := (b.N*personDirectoryPressureRate + channelMetaPressureRate - 1) / channelMetaPressureRate
	streams := make([][]int, len(nodes))
	for index := 0; index < personItems; index++ {
		nodeIndex := index % len(nodes)
		streams[nodeIndex] = append(streams[nodeIndex], index)
	}
	batches := make([]personDirectoryPressureBatch, 0, (personItems+batchItems-1)/batchItems)
	for nodeIndex, stream := range streams {
		for start := 0; start < len(stream); start += batchItems {
			end := min(start+batchItems, len(stream))
			indices := stream[start:end]
			last := indices[len(indices)-1]
			batches = append(batches, personDirectoryPressureBatch{
				nodeIndex: nodeIndex, indices: indices,
				sealAt: time.Duration(last) * time.Second / personDirectoryPressureRate,
			})
		}
	}
	sort.Slice(batches, func(i, j int) bool { return batches[i].sealAt < batches[j].sealAt })

	hotLatencies := make([]time.Duration, b.N)
	hotJobs := make(chan int, channelMetaPressureWorkers)
	personJobs := make(chan int, 24)
	semaphores := make([]chan struct{}, len(nodes))
	for index := range semaphores {
		semaphores[index] = make(chan struct{}, maxActive)
	}
	var workers sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	recordErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
	}
	workers.Add(channelMetaPressureWorkers)
	for range channelMetaPressureWorkers {
		go func() {
			defer workers.Done()
			for index := range hotJobs {
				started := time.Now()
				err := appendChannelMetaPressureMessage(
					nodes, index, fmt.Sprintf("person-pressure-%s-hot-%03d", namespace, index%channelMetaPressureHotChannels),
					messageIDBase+channelMetaPressureHotChannels+uint64(index+1), channelMetaPressurePayload,
				)
				hotLatencies[index] = time.Since(started)
				if err != nil {
					recordErr(fmt.Errorf("hot append index=%d: %w", index, err))
				}
			}
		}()
	}
	workers.Add(24)
	for range 24 {
		go func() {
			defer workers.Done()
			for batchIndex := range personJobs {
				batch := batches[batchIndex]
				semaphore := semaphores[batch.nodeIndex]
				semaphore <- struct{}{}
				_, err := admitPersonDirectoryPressureBatch(nodes[batch.nodeIndex], namespace, batch.indices)
				<-semaphore
				if err != nil {
					recordErr(fmt.Errorf("person-directory batch=%d: %w", batchIndex, err))
				}
			}
		}()
	}

	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	var producers sync.WaitGroup
	producers.Add(2)
	go func() {
		defer producers.Done()
		for index := 0; index < b.N; index++ {
			waitChannelMetaPressureUntil(started.Add(time.Duration(index) * time.Second / channelMetaPressureRate))
			hotJobs <- index
		}
		close(hotJobs)
	}()
	go func() {
		defer producers.Done()
		for batchIndex, batch := range batches {
			waitChannelMetaPressureUntil(started.Add(batch.sealAt))
			personJobs <- batchIndex
		}
		close(personJobs)
	}()
	producers.Wait()
	workers.Wait()
	b.StopTimer()
	if firstErr != nil {
		b.Fatal(firstErr)
	}
	reportChannelMetaPressureLatency(b, "hot-with-person-directory", hotLatencies)
	b.ReportMetric(float64(len(batches)), "person-directory-batches")
}

type channelMetaPressureBenchmarkOptions struct {
	coldEvery int
	payload   func(int) []byte
}

func benchmarkThreeNodeChannelAppendWithSlotMetaPressure1000QPS(b *testing.B, opts channelMetaPressureBenchmarkOptions) {
	if opts.coldEvery <= 0 {
		opts.coldEvery = channelMetaPressureColdEvery
	}
	if opts.payload == nil {
		opts.payload = channelMetaPressurePayload
	}
	observer := newChannelMetaPressureObserver()
	nodes := newChannelMetaPressureCluster(b, observer)
	startNodes(b, nodes...)
	b.Cleanup(func() { stopNodes(b, nodes...) })
	waitClusterReady(b, nodes...)
	waitChannelMetaPlacementReady(b, nodes)
	waitChannelMetaSlotsReady(b, nodes, 12)

	for index := 0; index < channelMetaPressureHotChannels; index++ {
		if err := appendChannelMetaPressureMessage(nodes, index, fmt.Sprintf("hot-%03d", index), uint64(index+1), opts.payload); err != nil {
			b.Fatalf("warm hot channel %d: %v", index, err)
		}
	}

	latencies := make([]time.Duration, b.N)
	cold := make([]bool, b.N)
	jobs := make(chan int, channelMetaPressureWorkers)
	var workers sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	workers.Add(channelMetaPressureWorkers)
	for range channelMetaPressureWorkers {
		go func() {
			defer workers.Done()
			for index := range jobs {
				isCold := index%opts.coldEvery == opts.coldEvery-1
				channelID := fmt.Sprintf("hot-%03d", index%channelMetaPressureHotChannels)
				if isCold {
					channelID = fmt.Sprintf("cold-%09d", index)
				}
				started := time.Now()
				err := appendChannelMetaPressureMessage(nodes, index, channelID, uint64(channelMetaPressureHotChannels+index+1), opts.payload)
				latencies[index] = time.Since(started)
				cold[index] = isCold
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("append index=%d cold=%t: %w", index, isCold, err)
					}
					errMu.Unlock()
				}
			}
		}()
	}

	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for index := 0; index < b.N; index++ {
		waitChannelMetaPressureUntil(started.Add(time.Duration(index) * time.Second / channelMetaPressureRate))
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	b.StopTimer()
	if firstErr != nil {
		b.Fatal(firstErr)
	}

	hotLatencies := make([]time.Duration, 0, b.N)
	coldLatencies := make([]time.Duration, 0, b.N/opts.coldEvery+1)
	for index, latency := range latencies {
		if cold[index] {
			coldLatencies = append(coldLatencies, latency)
		} else {
			hotLatencies = append(hotLatencies, latency)
		}
	}
	reportChannelMetaPressureLatency(b, "overall", latencies)
	reportChannelMetaPressureLatency(b, "hot", hotLatencies)
	reportChannelMetaPressureLatency(b, "cold", coldLatencies)
	observer.report(b)
}

func newChannelMetaPressureCluster(tb testing.TB, observer *channelMetaPressureObserver) []*Node {
	tb.Helper()
	addrs := []string{channelMetaPressureAddr(tb), channelMetaPressureAddr(tb), channelMetaPressureAddr(tb)}
	snapshot := channelMetaPressureSnapshot(addrs)
	voters := make([]ControlVoter, len(addrs))
	for index, addr := range addrs {
		voters[index] = ControlVoter{NodeID: uint64(index + 1), Addr: addr}
	}
	nodes := make([]*Node, 0, len(addrs))
	for index, addr := range addrs {
		nodeID := uint64(index + 1)
		cfg := Config{NodeID: nodeID, ListenAddr: addr, DataDir: tb.TempDir()}
		cfg.Control.ClusterID = snapshot.ClusterID
		cfg.Control.Voters = voters
		cfg.Slots.InitialSlotCount = uint32(len(snapshot.Slots))
		cfg.Slots.HashSlotCount = snapshot.HashSlots.Count
		cfg.Slots.ReplicaCount = 3
		cfg.Slots.TickInterval = 10 * time.Millisecond
		cfg.Slots.ElectionTick = 100
		cfg.Slots.HeartbeatTick = 1
		cfg.Channel.TickInterval = time.Millisecond
		cfg.Channel.Observer = observer
		node, err := New(cfg)
		if err != nil {
			tb.Fatalf("New(node=%d): %v", nodeID, err)
		}
		if err := node.ensureDefaultTransport(); err != nil {
			tb.Fatalf("ensureDefaultTransport(node=%d): %v", nodeID, err)
		}
		node.control = control.NewStaticController(snapshot)
		nodes = append(nodes, node)
	}
	return nodes
}

type channelMetaPressureObserver struct {
	mu        sync.Mutex
	stages    map[string][]time.Duration
	batchSize []int
}

func newChannelMetaPressureObserver() *channelMetaPressureObserver {
	return &channelMetaPressureObserver{stages: make(map[string][]time.Duration)}
}

func (o *channelMetaPressureObserver) reset() {
	o.mu.Lock()
	o.stages = make(map[string][]time.Duration)
	o.batchSize = nil
	o.mu.Unlock()
}

func (*channelMetaPressureObserver) SetReactorMailboxDepth(int, string, int) {}
func (*channelMetaPressureObserver) SetWorkerQueueDepth(string, int)         {}
func (*channelMetaPressureObserver) ObserveAppendBatch(int, int, time.Duration) {
}
func (*channelMetaPressureObserver) ObserveAppendLatency(channelruntime.CommitMode, time.Duration) {
}
func (*channelMetaPressureObserver) ObserveWorkerResult(channelworker.TaskKind, error, time.Duration) {
}
func (o *channelMetaPressureObserver) ObserveChannelAppendStage(stage, result string, d time.Duration) {
	if result != "ok" && result != "miss" {
		return
	}
	o.mu.Lock()
	o.stages[stage] = append(o.stages[stage], d)
	o.mu.Unlock()
}
func (*channelMetaPressureObserver) ObserveChannelMetaCreateCoalesced(uint32) {}
func (*channelMetaPressureObserver) SetChannelMetaCreateQueueDepth(uint32, int) {
}
func (o *channelMetaPressureObserver) ObserveChannelMetaCreateBatch(_ uint32, result string, items int) {
	if result != "ok" && result != "recovered" {
		return
	}
	o.mu.Lock()
	o.batchSize = append(o.batchSize, items)
	o.mu.Unlock()
}

func (o *channelMetaPressureObserver) report(b *testing.B) {
	b.Helper()
	o.mu.Lock()
	stages := make(map[string][]time.Duration, len(o.stages))
	for stage, values := range o.stages {
		stages[stage] = append([]time.Duration(nil), values...)
	}
	batchSizes := append([]int(nil), o.batchSize...)
	o.mu.Unlock()
	for _, stage := range []string{
		"meta_slot_read", "meta_create_build", "meta_create_propose", "meta_final_read", "meta_create_write",
		"meta_create_slot_propose_submit", "meta_create_slot_propose_wait", "meta_create_slot_control_wait",
		"meta_create_slot_raft_commit_wait", "meta_create_slot_fsm_apply", "meta_create_slot_fsm_commit",
		"meta_create_slot_mark_applied",
	} {
		values := stages[stage]
		if len(values) == 0 {
			continue
		}
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		b.ReportMetric(float64(values[(len(values)-1)*99/100])/float64(time.Millisecond), stage+"-p99-ms")
		if stage == "meta_create_slot_propose_submit" {
			b.ReportMetric(float64(len(values)), stage+"-count")
			b.ReportMetric(float64(b.N)/float64(len(values)), "items-per-slot-proposal")
		}
	}
	if len(batchSizes) == 0 {
		return
	}
	total := 0
	for _, size := range batchSizes {
		total += size
	}
	b.ReportMetric(float64(total)/float64(len(batchSizes)), "meta-batch-avg-items")
	b.ReportMetric(float64(len(batchSizes)), "meta-batches")
}

func channelMetaPressureSnapshot(addrs []string) control.Snapshot {
	const slotCount = 12
	snapshot := control.Snapshot{
		ClusterID: "channel-meta-pressure",
		Revision:  1, ControllerID: 1,
		HashSlots:             control.HashSlotTable{Revision: 1, Count: 256},
		ChannelDataPlaneLease: control.ChannelDataPlaneLease{LastVisibleAt: time.Now(), TTL: time.Minute, Ready: true},
	}
	for index, addr := range addrs {
		nodeID := uint64(index + 1)
		snapshot.Nodes = append(snapshot.Nodes, control.Node{
			NodeID: nodeID, Addr: addr, Roles: []control.Role{control.RoleData},
			Status: control.NodeAlive, JoinState: control.NodeJoinStateActive, CapacityWeight: 1,
			Health: control.NodeHealth{Status: control.NodeAlive, Freshness: control.NodeHealthFresh, RuntimeReady: true, ObservedControlRevision: 1, ObservedSlotRevision: 1},
		})
	}
	for index := 0; index < slotCount; index++ {
		from := uint16(index * 256 / slotCount)
		to := uint16((index+1)*256/slotCount - 1)
		snapshot.HashSlots.Ranges = append(snapshot.HashSlots.Ranges, control.HashSlotRange{From: from, To: to, SlotID: uint32(index + 1)})
		snapshot.Slots = append(snapshot.Slots, control.SlotAssignment{
			SlotID: uint32(index + 1), DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, PreferredLeader: uint64(index%3 + 1),
		})
	}
	return snapshot
}

func channelMetaPressureAddr(tb testing.TB) string {
	tb.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		tb.Fatal(err)
	}
	return addr
}

func waitChannelMetaPlacementReady(tb testing.TB, nodes []*Node) {
	tb.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ready := true
		for _, node := range nodes {
			if len(node.channelDataNodes.DataNodes()) != len(nodes) {
				ready = false
				break
			}
		}
		if ready {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	tb.Fatal("Channel placement data nodes did not become ready")
}

func waitChannelMetaSlotsReady(tb testing.TB, nodes []*Node, slotCount int) {
	tb.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ready := true
		for _, node := range nodes {
			for slotID := 1; slotID <= slotCount; slotID++ {
				status, err := node.defaultSlotRuntime.Status(multiraft.SlotID(slotID))
				if err != nil || status.LeaderID == 0 || len(status.CurrentVoters) != len(nodes) {
					ready = false
					break
				}
			}
			if !ready {
				break
			}
		}
		if ready {
			ready = channelMetaSlotProxyReadsReady(nodes, slotCount)
		}
		if ready {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	var summary string
	for _, node := range nodes {
		summary += fmt.Sprintf(" node=%d[", node.NodeID())
		for slotID := 1; slotID <= slotCount; slotID++ {
			status, err := node.defaultSlotRuntime.Status(multiraft.SlotID(slotID))
			if err != nil {
				summary += fmt.Sprintf("s%d:%v,", slotID, err)
				continue
			}
			summary += fmt.Sprintf("s%d:l%d/v%d,", slotID, status.LeaderID, len(status.CurrentVoters))
		}
		summary += "]"
	}
	tb.Fatalf("Slot runtime did not converge before benchmark:%s", summary)
}

func channelMetaSlotProxyReadsReady(nodes []*Node, slotCount int) bool {
	if len(nodes) == 0 || slotCount <= 0 {
		return false
	}
	keys := make(map[uint32]string, slotCount)
	for candidate := 0; candidate < 10000 && len(keys) < slotCount; candidate++ {
		key := fmt.Sprintf("meta-pressure-readiness-%05d", candidate)
		route, err := nodes[0].RouteKey(key)
		if err == nil && route.SlotID > 0 && int(route.SlotID) <= slotCount {
			keys[route.SlotID] = key
		}
	}
	if len(keys) != slotCount {
		return false
	}
	for _, node := range nodes {
		for _, key := range keys {
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			_, err := node.GetChannelRuntimeMeta(ctx, key, 2)
			cancel()
			if err != nil && !errors.Is(err, metadb.ErrNotFound) {
				return false
			}
		}
	}
	return true
}

func appendColdPersonChannelPressureMessage(nodes []*Node, index int) error {
	left := fmt.Sprintf("cold-person-left-%09d", index)
	right := fmt.Sprintf("cold-person-right-%09d", index)
	channelID := runtimechannelid.EncodePersonChannel(left, right)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	messageID := uint64(index + 1)
	result, err := nodes[index%len(nodes)].AppendChannel(ctx, channelruntime.AppendRequest{
		ChannelID: channelruntime.ChannelID{ID: channelID, Type: 1},
		Message: channelruntime.Message{
			MessageID: messageID, ChannelID: channelID, ChannelType: 1,
			FromUID: left, ClientMsgNo: fmt.Sprintf("cold-person-meta-pressure-%d", messageID), Payload: channelMetaPressurePayload(index),
		},
		CommitMode: channelruntime.CommitModeQuorum,
	})
	if err != nil {
		return err
	}
	if result.MessageID != messageID || result.MessageSeq == 0 {
		return fmt.Errorf("result=%#v", result)
	}
	return nil
}

func appendChannelMetaPressureMessage(nodes []*Node, index int, channelID string, messageID uint64, payload func(int) []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := nodes[index%len(nodes)].AppendChannel(ctx, channelruntime.AppendRequest{
		ChannelID: channelruntime.ChannelID{ID: channelID, Type: 2},
		Message: channelruntime.Message{
			MessageID: messageID, ChannelID: channelID, ChannelType: 2,
			FromUID: "benchmark-sender", ClientMsgNo: fmt.Sprintf("meta-pressure-%d", messageID), Payload: payload(index),
		},
		CommitMode: channelruntime.CommitModeQuorum,
	})
	if err != nil {
		return err
	}
	if result.MessageID != messageID || result.MessageSeq == 0 {
		return fmt.Errorf("result=%#v", result)
	}
	return nil
}

func channelMetaPressurePayload(index int) []byte {
	size := 256
	switch index % 100 {
	case 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93, 94:
		size = 1024
	case 95, 96, 97, 98:
		size = 4 * 1024
	case 99:
		size = 16 * 1024
	}
	return make([]byte, size)
}

func waitChannelMetaPressureUntil(deadline time.Time) {
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		if remaining > 200*time.Microsecond {
			time.Sleep(remaining - 100*time.Microsecond)
			continue
		}
	}
}

func reportChannelMetaPressureLatency(b *testing.B, name string, latencies []time.Duration) {
	b.Helper()
	if len(latencies) == 0 {
		return
	}
	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p99 := sorted[(len(sorted)*99-1)/100]
	above := 0
	for _, latency := range sorted {
		if latency > 200*time.Millisecond {
			above++
		}
	}
	b.ReportMetric(float64(p99)/float64(time.Millisecond), name+"-p99-ms")
	b.ReportMetric(100*float64(above)/float64(len(sorted)), name+"-over-200ms-pct")
}
