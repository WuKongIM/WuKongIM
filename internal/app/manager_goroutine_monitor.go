package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
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
	ManagerGoroutineSnapshot(context.Context, uint64) (managementusecase.GoroutineSnapshot, error)
}

type managerGoroutineLocalReader struct {
	registry *goruntimeregistry.Registry
}

func (r managerGoroutineLocalReader) Snapshot() managementusecase.GoroutineSnapshot {
	if r.registry == nil {
		return managementusecase.GoroutineSnapshot{}
	}
	return projectGoroutineSnapshot(r.registry.Snapshot())
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
	snapshot managementusecase.GoroutineSnapshot
	readAt   time.Time
}

type managerGoroutineInflight struct {
	done     chan struct{}
	snapshot managementusecase.GoroutineSnapshot
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
	} else if nodeID == 0 {
		m.retainCurrentNodeCache(nodes)
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
		snapshot := projectGoroutineSnapshot(m.registry.Snapshot())
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

func (m *managerGoroutineMonitor) readRemote(ctx context.Context, nodeID uint64) (snapshot managementusecase.GoroutineSnapshot, err error) {
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
			return managementusecase.GoroutineSnapshot{}, ctx.Err()
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
			snapshot = managementusecase.GoroutineSnapshot{}
			err = fmt.Errorf("remote snapshot panic")
		}
		running.snapshot = snapshot
		running.err = err
		m.cacheMu.Lock()
		if err == nil {
			m.storeCacheLocked(nodeID, managerGoroutineCacheEntry{snapshot: snapshot, readAt: time.Now()})
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
	case errors.Is(err, clusternet.ErrServiceNotFound):
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
				if task.Kind == string(goruntimeregistry.TaskKindPool) &&
					task.Health == goruntimeregistry.HealthCritical &&
					task.HealthReason == "saturated" {
					saturatedPools++
				}
			}
		}
	}
	return []accessmanager.RealtimeMonitorSnapshotEntry{
		{Key: "goroutineProcessTotal", Value: float64(processTotal), Unit: "goroutines", Tone: accessmanager.RealtimeMonitorToneNormal, Source: "direct_node_rpc"},
		{Key: "goroutineManagedTotal", Value: float64(managedTotal), Unit: "goroutines", Tone: accessmanager.RealtimeMonitorToneNormal, Source: "direct_node_rpc"},
		{Key: "goroutineUnmanagedTotal", Value: float64(unmanagedTotal), Unit: "goroutines", Tone: accessmanager.RealtimeMonitorToneNormal, Source: "direct_node_rpc"},
		{Key: "goroutinePanicTotal", Value: float64(panicTotal), Unit: "panics", Tone: summaryTone(panicTotal), Source: "direct_node_rpc"},
		{Key: "goroutineSaturatedPools", Value: float64(saturatedPools), Unit: "pools", Tone: summaryTone(saturatedPools), Source: "direct_node_rpc"},
	}
}

func (m *managerGoroutineMonitor) retainCurrentNodeCache(nodes []managementusecase.Node) {
	if m == nil {
		return
	}
	current := make(map[uint64]struct{}, len(nodes))
	for _, node := range nodes {
		current[node.NodeID] = struct{}{}
	}
	m.cacheMu.Lock()
	for nodeID := range m.cache {
		if _, ok := current[nodeID]; !ok {
			delete(m.cache, nodeID)
		}
	}
	m.cacheMu.Unlock()
}

func (m *managerGoroutineMonitor) storeCacheLocked(nodeID uint64, entry managerGoroutineCacheEntry) {
	if _, exists := m.cache[nodeID]; !exists && len(m.cache) >= managerGoroutineMaxNodes {
		var oldestNodeID uint64
		var oldestReadAt time.Time
		for cachedNodeID, cached := range m.cache {
			if oldestReadAt.IsZero() || cached.readAt.Before(oldestReadAt) {
				oldestNodeID = cachedNodeID
				oldestReadAt = cached.readAt
			}
		}
		delete(m.cache, oldestNodeID)
	}
	m.cache[nodeID] = entry
}

func projectGoroutineSnapshot(snapshot goruntimeregistry.Snapshot) managementusecase.GoroutineSnapshot {
	out := managementusecase.GoroutineSnapshot{
		GeneratedAt:      snapshot.GeneratedAt,
		ProcessStartedAt: snapshot.ProcessStartedAt,
		BootID:           snapshot.BootID,
		ProcessTotal:     snapshot.ProcessTotal,
		ManagedTotal:     snapshot.ManagedTotal,
		UnmanagedTotal:   snapshot.UnmanagedTotal,
		Reconciled:       snapshot.Reconciled,
		TotalActive:      snapshot.TotalActive,
		TotalStarted:     snapshot.TotalStarted,
		TotalPanics:      snapshot.TotalPanics,
		Modules:          make([]managementusecase.GoroutineModuleSnapshot, len(snapshot.Modules)),
	}
	for moduleIndex, module := range snapshot.Modules {
		projectedModule := managementusecase.GoroutineModuleSnapshot{
			Module:        string(module.Module),
			Active:        module.Active,
			ProcessPeak:   module.Peak,
			TotalStarted:  module.TotalStarted,
			TotalStopped:  module.TotalStopped,
			Panics:        module.PanicCount,
			BusyTasks:     module.BusyTasks,
			PoolCapacity:  module.PoolCapacity,
			QueueDepth:    module.QueueDepth,
			QueueCapacity: module.QueueCapacity,
			RejectedTotal: module.RejectedTotal,
			Tasks:         make([]managementusecase.GoroutineTaskSnapshot, len(module.Tasks)),
			Health:        module.Health,
		}
		for taskIndex, task := range module.Tasks {
			projectedModule.Tasks[taskIndex] = managementusecase.GoroutineTaskSnapshot{
				Task:          string(task.Task),
				Name:          task.Name,
				Kind:          string(task.Kind),
				Critical:      task.Critical,
				Expected:      task.Expected,
				Active:        task.Active,
				ProcessPeak:   task.Peak,
				TotalStarted:  task.TotalStarted,
				TotalStopped:  task.TotalStopped,
				Panics:        task.PanicCount,
				BusyTasks:     task.BusyTasks,
				PoolCapacity:  task.PoolCapacity,
				QueueDepth:    task.QueueDepth,
				QueueCapacity: task.QueueCapacity,
				RejectedTotal: task.RejectedTotal,
				RunningFor:    task.RunningFor,
				Health:        task.Health,
				HealthReason:  task.HealthReason,
			}
		}
		out.Modules[moduleIndex] = projectedModule
	}
	return out
}

func summaryTone(value int64) string {
	if value > 0 {
		return accessmanager.RealtimeMonitorToneCritical
	}
	return accessmanager.RealtimeMonitorToneNormal
}
