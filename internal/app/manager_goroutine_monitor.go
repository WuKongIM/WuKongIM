package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const (
	managerGoroutineFanoutConcurrency = 8
	managerGoroutineFanoutTimeout     = 2 * time.Second
	managerGoroutineNodeTimeout       = 1500 * time.Millisecond
	managerGoroutineCacheTTL          = 2 * time.Second
	managerGoroutineStaleAfter        = 15 * time.Second
	managerGoroutineMaxNodes          = 256
)

type managerGoroutineSnapshotReader interface {
	ManagerGoroutineSnapshot(context.Context, uint64) (goruntimeregistry.Snapshot, error)
}

type managerGoroutineMonitor struct {
	localNodeID uint64
	registry    *goruntimeregistry.Registry
	control     managerClusterControlReader
	remote      managerGoroutineSnapshotReader
	cacheMu     sync.Mutex
	cache       map[uint64]managerGoroutineCacheEntry
	inflight    map[uint64]*managerGoroutineInflight
}

type managerGoroutineCacheEntry struct {
	snapshot goruntimeregistry.Snapshot
	readAt   time.Time
}

type managerGoroutineInflight struct {
	done     chan struct{}
	snapshot goruntimeregistry.Snapshot
	err      error
}

type managerRealtimeMonitorProvider struct {
	base       accessmanager.RealtimeMonitorProvider
	goroutines *managerGoroutineMonitor
}

func (p *managerRealtimeMonitorProvider) RealtimeMonitor(ctx context.Context, query accessmanager.RealtimeMonitorQuery) (accessmanager.RealtimeMonitorResponse, error) {
	response, err := p.base.RealtimeMonitor(ctx, query)
	if err != nil {
		return response, err
	}
	if query.Category != accessmanager.RealtimeMonitorCategoryGoroutines {
		return response, nil
	}
	prometheusStatus := response.Status
	direct, source := p.goroutines.snapshot(ctx, query.NodeID)
	response.Goroutines = &direct
	response.Sources.Goroutines = &source
	response.Snapshot = goroutineSummary(direct)
	response.Status = direct.Status
	if response.Status == accessmanager.RealtimeMonitorStatusReady &&
		prometheusStatus != accessmanager.RealtimeMonitorStatusReady &&
		prometheusStatus != accessmanager.RealtimeMonitorStatusPrometheusDisabled {
		response.Status = accessmanager.RealtimeMonitorStatusPartial
	}
	return response, nil
}

func (m *managerGoroutineMonitor) snapshot(ctx context.Context, nodeID uint64) (accessmanager.RealtimeMonitorGoroutines, accessmanager.RealtimeMonitorSource) {
	started := time.Now()
	result := accessmanager.RealtimeMonitorGoroutines{
		Status:      accessmanager.RealtimeMonitorStatusReady,
		GeneratedAt: time.Now().UTC(),
		Nodes:       []accessmanager.RealtimeMonitorGoroutineNode{},
	}
	nodes, listErr := m.nodes(ctx, nodeID)
	if listErr != nil {
		result.Status = accessmanager.RealtimeMonitorStatusPartial
	}
	if len(nodes) == 0 && m != nil && m.localNodeID != 0 && (nodeID == 0 || nodeID == m.localNodeID) {
		nodes = append(nodes, managementusecase.Node{NodeID: m.localNodeID, Name: fmt.Sprintf("node-%d", m.localNodeID), Status: "local"})
	}

	fanoutCtx, cancelFanout := context.WithTimeout(ctx, managerGoroutineFanoutTimeout)
	defer cancelFanout()
	result.Nodes = make([]accessmanager.RealtimeMonitorGoroutineNode, len(nodes))
	for i, node := range nodes {
		result.Nodes[i] = accessmanager.RealtimeMonitorGoroutineNode{
			NodeID: node.NodeID,
			Name:   node.Name,
			Status: node.Status,
			Error:  "timeout",
		}
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := len(nodes)
	if workers > managerGoroutineFanoutConcurrency {
		workers = managerGoroutineFanoutConcurrency
	}
	for workerID := 0; workerID < workers; workerID++ {
		wg.Add(1)
		goruntimeregistry.SafeGo(m.registry, goruntimeregistry.TaskManagerSnapshotFanout, func() {
			defer wg.Done()
			for i := range jobs {
				result.Nodes[i] = m.readNode(fanoutCtx, nodes[i])
			}
		})
	}
submit:
	for i := range nodes {
		select {
		case jobs <- i:
		case <-fanoutCtx.Done():
			break submit
		}
	}
	close(jobs)
	wg.Wait()

	available := 0
	for _, node := range result.Nodes {
		if node.Supported && node.Snapshot != nil {
			available++
		} else {
			result.Status = accessmanager.RealtimeMonitorStatusPartial
		}
	}
	source := accessmanager.RealtimeMonitorSource{
		Enabled: available > 0,
		QueryMS: time.Since(started).Milliseconds(),
	}
	if listErr != nil {
		source.Error = fmt.Sprintf("list cluster nodes: %v", listErr)
	}
	if available == 0 && source.Error == "" {
		source.Error = "no node supports managed goroutine snapshots"
	}
	if available == 0 {
		result.Status = accessmanager.RealtimeMonitorStatusPartial
	}
	return result, source
}

func (m *managerGoroutineMonitor) nodes(ctx context.Context, nodeID uint64) ([]managementusecase.Node, error) {
	if m == nil || m.control == nil {
		return nil, nil
	}
	list, err := m.control.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	nodes := make([]managementusecase.Node, 0, len(list.Items))
	for _, node := range list.Items {
		if nodeID == 0 || node.NodeID == nodeID {
			nodes = append(nodes, node)
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	if len(nodes) > managerGoroutineMaxNodes {
		return nodes[:managerGoroutineMaxNodes], fmt.Errorf("node snapshot response truncated at %d nodes", managerGoroutineMaxNodes)
	}
	return nodes, nil
}

func (m *managerGoroutineMonitor) readNode(ctx context.Context, node managementusecase.Node) accessmanager.RealtimeMonitorGoroutineNode {
	out := accessmanager.RealtimeMonitorGoroutineNode{
		NodeID: node.NodeID,
		Name:   node.Name,
		Status: node.Status,
	}
	if m != nil && node.NodeID == m.localNodeID && m.registry != nil {
		snapshot := m.registry.Snapshot()
		out.Supported = true
		out.Snapshot = &snapshot
		return out
	}
	if m == nil || m.remote == nil {
		out.Error = "unsupported"
		return out
	}
	nodeCtx, cancel := context.WithTimeout(ctx, managerGoroutineNodeTimeout)
	defer cancel()
	snapshot, err := m.readRemote(nodeCtx, node.NodeID)
	if err != nil {
		out.Error = managerGoroutineReadError(err)
		return out
	}
	out.Supported = true
	if snapshot.GeneratedAt.IsZero() || time.Since(snapshot.GeneratedAt) > managerGoroutineStaleAfter || time.Until(snapshot.GeneratedAt) > managerGoroutineStaleAfter {
		out.Error = "stale"
		return out
	}
	out.Snapshot = &snapshot
	return out
}

func (m *managerGoroutineMonitor) readRemote(ctx context.Context, nodeID uint64) (snapshot goruntimeregistry.Snapshot, err error) {
	now := time.Now()
	m.cacheMu.Lock()
	if cached, ok := m.cache[nodeID]; ok && now.Sub(cached.readAt) <= managerGoroutineCacheTTL {
		m.cacheMu.Unlock()
		return cached.snapshot, nil
	}
	if running := m.inflight[nodeID]; running != nil {
		m.cacheMu.Unlock()
		select {
		case <-running.done:
			return running.snapshot, running.err
		case <-ctx.Done():
			return goruntimeregistry.Snapshot{}, ctx.Err()
		}
	}
	if m.cache == nil {
		m.cache = make(map[uint64]managerGoroutineCacheEntry)
	}
	if m.inflight == nil {
		m.inflight = make(map[uint64]*managerGoroutineInflight)
	}
	running := &managerGoroutineInflight{done: make(chan struct{})}
	m.inflight[nodeID] = running
	m.cacheMu.Unlock()

	defer func() {
		if recovered := recover(); recovered != nil {
			snapshot = goruntimeregistry.Snapshot{}
			err = fmt.Errorf("remote snapshot panic")
		}
		running.snapshot = snapshot
		running.err = err
		m.cacheMu.Lock()
		if err == nil {
			m.cache[nodeID] = managerGoroutineCacheEntry{snapshot: snapshot, readAt: time.Now()}
		}
		delete(m.inflight, nodeID)
		close(running.done)
		m.cacheMu.Unlock()
	}()
	return m.remote.ManagerGoroutineSnapshot(ctx, nodeID)
}

func managerGoroutineReadError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case strings.Contains(strings.ToLower(err.Error()), "unknown"),
		strings.Contains(strings.ToLower(err.Error()), "unsupported"),
		strings.Contains(strings.ToLower(err.Error()), "not found"):
		return "unsupported"
	default:
		return "unavailable"
	}
}

func goroutineSummary(snapshot accessmanager.RealtimeMonitorGoroutines) []accessmanager.RealtimeMonitorSnapshotEntry {
	var processTotal, managedTotal, unmanagedTotal, panicTotal, saturatedPools int64
	for _, node := range snapshot.Nodes {
		if node.Snapshot == nil {
			continue
		}
		processTotal += node.Snapshot.ProcessTotal
		managedTotal += node.Snapshot.ManagedTotal
		unmanagedTotal += node.Snapshot.UnmanagedTotal
		panicTotal += node.Snapshot.TotalPanics
		for _, module := range node.Snapshot.Modules {
			for _, task := range module.Tasks {
				if task.Kind == goruntimeregistry.TaskKindPool &&
					(task.RejectedTotal > 0 || (task.PoolCapacity > 0 && task.BusyTasks >= task.PoolCapacity && task.QueueDepth > 0)) {
					saturatedPools++
				}
			}
		}
	}
	return []accessmanager.RealtimeMonitorSnapshotEntry{
		{Key: "goroutineProcessTotal", Value: float64(processTotal), Unit: "goroutines", Tone: accessmanager.RealtimeMonitorToneNormal, Source: "direct_node_rpc"},
		{Key: "goroutineManagedTotal", Value: float64(managedTotal), Unit: "goroutines", Tone: accessmanager.RealtimeMonitorToneNormal, Source: "direct_node_rpc"},
		{Key: "goroutineUnmanagedTotal", Value: float64(unmanagedTotal), Unit: "goroutines", Tone: accessmanager.RealtimeMonitorToneWarning, Source: "direct_node_rpc"},
		{Key: "goroutinePanicTotal", Value: float64(panicTotal), Unit: "panics", Tone: summaryTone(panicTotal), Source: "direct_node_rpc"},
		{Key: "goroutineSaturatedPools", Value: float64(saturatedPools), Unit: "pools", Tone: summaryTone(saturatedPools), Source: "direct_node_rpc"},
	}
}

func summaryTone(value int64) string {
	if value > 0 {
		return accessmanager.RealtimeMonitorToneCritical
	}
	return accessmanager.RealtimeMonitorToneNormal
}
