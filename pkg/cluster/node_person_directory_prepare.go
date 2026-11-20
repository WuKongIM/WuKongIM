package cluster

import (
	"context"
	"encoding/json"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"sync"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

const personPrepareMaxItems = 64
const personPrepareMaxBytes = 256 << 10

type personPrepareRequest struct {
	Version int                                  `json:"version"`
	Tasks   []metadb.PersonDirectoryTaskLocation `json:"tasks"`
}
type personPrepareResponse struct {
	Version int                                 `json:"version"`
	Ready   []metadb.PersonDirectoryAppendFence `json:"ready"`
}

// SetPersonDirectoryPreparation binds the app-owned bounded projector before
// start. Local and remote callers share eight execution permits and at most
// 32 retained requests (8 MiB of payloads). Queue time consumes the same four-
// second attempt budget; cancellation releases ownership before projection.
// The callback owns no transport or metadata lifecycle.
func (n *Node) SetPersonDirectoryPreparation(prepare func(context.Context, []metadb.PersonDirectoryTaskEntry) []error) {
	gate := make(chan struct{}, 8)
	owned := make(chan struct{}, 32)
	n.RegisterRPC(clusternet.RPCPersonDirectoryPrepare, clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		if len(payload) > personPrepareMaxBytes || prepare == nil {
			return nil, metadb.ErrInvalidArgument
		}
		ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		select {
		case owned <- struct{}{}:
			defer func() { <-owned }()
		default:
			return nil, ErrRouteNotReady
		}
		if err := n.ensureForeground(); err != nil {
			return nil, err
		}
		var request personPrepareRequest
		if err := json.Unmarshal(payload, &request); err != nil {
			return nil, err
		}
		if request.Version != 1 || len(request.Tasks) == 0 || len(request.Tasks) > personPrepareMaxItems {
			return nil, metadb.ErrInvalidArgument
		}
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return n.preparePersonDirectoryLocal(ctx, request.Tasks, prepare)
	}))
}

// PreparePersonDirectoryTaskWaves admits missing source tasks and dispatches
// each source-node group when its own admissions finish. Pending tasks retain
// their original tail and generation. Only a fresh Ready proof releases SEND.
func (n *Node) PreparePersonDirectoryTaskWaves(ctx context.Context, tasks []metadb.PersonDirectoryTask, emit func(int, metadb.PersonDirectoryAppendFence, error)) {
	if len(tasks) == 0 {
		return
	}
	if len(tasks) > 128 {
		for i := range tasks {
			emit(i, metadb.PersonDirectoryAppendFence{}, metadb.ErrInvalidArgument)
		}
		return
	}
	keys := make([]string, len(tasks))
	for i, task := range tasks {
		keys[i] = task.ChannelID
	}
	routes, err := n.RouteKeysPartial(keys)
	if err != nil {
		for i := range tasks {
			emit(i, metadb.PersonDirectoryAppendFence{}, err)
		}
		return
	}
	type group struct {
		nodeID  uint64
		indices []int
		tasks   []metadb.PersonDirectoryTaskLocation
		// waiting counts only this source-node group's unfinished admissions.
		waiting int
	}
	type outcome struct {
		fence metadb.PersonDirectoryAppendFence
		index int
		err   error
	}
	groups := make(map[uint64]*group)
	byIndex := make([]*group, len(tasks))
	results := make([]error, len(tasks))
	missing := make([]metadb.PersonDirectoryTask, 0, len(tasks))
	indices := make([]int, 0, len(tasks))
	completed := 0
	for i, route := range routes {
		if route.Err != nil || route.Route.Leader == 0 {
			emit(i, metadb.PersonDirectoryAppendFence{}, ErrRouteNotReady)
			completed++
			continue
		}
		nodeID := route.Route.Leader
		g := groups[nodeID]
		if g == nil {
			g = &group{nodeID: nodeID}
			groups[nodeID] = g
		}
		byIndex[i] = g
		g.indices = append(g.indices, i)
		g.tasks = append(g.tasks, metadb.PersonDirectoryTaskLocation{HashSlot: metadb.HashSlot(route.Route.HashSlot), ChannelID: tasks[i].ChannelID, ChannelType: tasks[i].ChannelType, Generation: tasks[i].Generation})
		if tasks[i].Generation == 0 {
			g.waiting++
			missing = append(missing, tasks[i])
			indices = append(indices, i)
		}
	}
	// At most one job per original source-node chunk is retained, with the same
	// four RPC workers. A slow source never holds another source's ready job.
	jobs := make(chan group, len(tasks))
	done := make(chan outcome, len(tasks))
	var workers sync.WaitGroup
	jobCount := 0
	for _, g := range groups {
		jobCount += (len(g.tasks) + personPrepareMaxItems - 1) / personPrepareMaxItems
	}
	for range min(4, jobCount) {
		workers.Add(1)
		goruntimeregistry.SafeGo(n.cfg.Goroutines, goruntimeregistry.TaskMessageDirectoryBatch, func() {
			defer workers.Done()
			for g := range jobs {
				ready, err := n.callPersonDirectoryPreparation(ctx, g.nodeID, g.tasks)
				for j, index := range g.indices {
					itemErr := err
					if itemErr == nil && (j >= len(ready) || !ready[j].Valid()) {
						itemErr = ErrRouteNotReady
					}
					var fence metadb.PersonDirectoryAppendFence
					if itemErr == nil {
						fence = ready[j]
					}
					done <- outcome{index: index, fence: fence, err: itemErr}
				}
			}
		})
	}
	dispatch := func(g *group) {
		job := group{nodeID: g.nodeID}
		for j, index := range g.indices {
			if results[index] != nil {
				continue
			}
			job.indices = append(job.indices, index)
			job.tasks = append(job.tasks, g.tasks[j])
			if len(job.tasks) == personPrepareMaxItems {
				jobs <- job
				job = group{nodeID: g.nodeID}
			}
		}
		if len(job.tasks) > 0 {
			jobs <- job
		}
	}
	for _, g := range groups {
		if g.waiting == 0 {
			dispatch(g)
		}
	}
	// Admission already publishes independent Slot results. Consume them while
	// projection runs, but retain its owner until every submitted call has joined.
	admitted := make(chan outcome, len(missing))
	goruntimeregistry.SafeGo(n.cfg.Goroutines, goruntimeregistry.TaskMessageDirectoryBatch, func() {
		defer close(admitted)
		n.AdmitPersonDirectoryTaskWaves(ctx, missing, func(i int, err error) { admitted <- outcome{index: indices[i], err: err} })
	})
	for completed < len(tasks) || admitted != nil {
		select {
		case result, open := <-admitted:
			if !open {
				admitted = nil
				continue
			}
			results[result.index] = result.err
			if result.err != nil {
				emit(result.index, metadb.PersonDirectoryAppendFence{}, result.err)
				completed++
			}
			g := byIndex[result.index]
			g.waiting--
			if g.waiting == 0 {
				dispatch(g)
			}
		case result := <-done:
			emit(result.index, result.fence, result.err)
			completed++
		}
	}
	close(jobs)
	workers.Wait()
}

func (n *Node) callPersonDirectoryPreparation(ctx context.Context, nodeID uint64, tasks []metadb.PersonDirectoryTaskLocation) ([]metadb.PersonDirectoryAppendFence, error) {
	payload, err := json.Marshal(personPrepareRequest{Version: 1, Tasks: tasks})
	if err != nil {
		return nil, err
	}
	if len(payload) > personPrepareMaxBytes {
		return nil, metadb.ErrInvalidArgument
	}
	var response []byte
	if nodeID == n.cfg.NodeID {
		n.mu.RLock()
		handler := n.pendingRPCHandlers[clusternet.RPCPersonDirectoryPrepare]
		n.mu.RUnlock()
		if handler == nil {
			return nil, ErrRouteNotReady
		}
		response, err = handler.HandleRPC(ctx, payload)
	} else {
		response, err = n.CallRPC(ctx, nodeID, clusternet.RPCPersonDirectoryPrepare, payload)
	}
	if err != nil {
		return nil, err
	}
	var result personPrepareResponse
	if len(response) > personPrepareMaxBytes || json.Unmarshal(response, &result) != nil || result.Version != 1 || len(result.Ready) != len(tasks) {
		return nil, ErrRouteNotReady
	}
	return result.Ready, nil
}

func (n *Node) preparePersonDirectoryLocal(ctx context.Context, tasks []metadb.PersonDirectoryTaskLocation, prepare func(context.Context, []metadb.PersonDirectoryTaskEntry) []error) ([]byte, error) {
	if n.defaultSlotMetaDB == nil || n.defaultSlotProposer == nil {
		return nil, ErrNotStarted
	}
	keys := make([]string, len(tasks))
	for i, task := range tasks {
		if task.ChannelID == "" || task.ChannelType != 1 {
			return nil, metadb.ErrInvalidArgument
		}
		keys[i] = task.ChannelID
	}
	routes, err := n.RouteKeysPartial(keys)
	if err != nil {
		return nil, err
	}
	candidates := make([]metadb.PersonDirectoryTaskEntry, 0, len(tasks))
	expected := make([]uint64, len(tasks))
	valid := make([]bool, len(tasks))
	for i, route := range routes {
		if route.Err != nil || route.Route.HashSlot != uint16(tasks[i].HashSlot) || !n.defaultSlotProposer.IsLocalLeader(route.Route.SlotID) {
			continue
		}
		shard := n.defaultSlotMetaDB.ForHashSlot(route.Route.HashSlot)
		task, found, err := shard.GetPersonDirectoryTask(ctx, tasks[i].ChannelID, 1)
		if err != nil {
			continue
		}
		if found {
			if tasks[i].Generation != 0 && tasks[i].Generation != task.Generation {
				continue
			}
			expected[i] = task.Generation
			valid[i] = true
			candidates = append(candidates, metadb.PersonDirectoryTaskEntry{HashSlot: tasks[i].HashSlot, Task: task})
		} else {
			// Completion deletes the task and publishes Ready atomically, but a
			// concurrent Channel cache fill can still retain the earlier Pending
			// row. Capture the completed generation from storage, not that cache.
			// This is only a candidate: the fresh authoritative batch below must
			// still prove the same generation and complete runtime append fence.
			facts, err := n.defaultSlotMetaDB.ReadPermissionSnapshot(ctx, []metadb.PermissionFactRead{{
				Kind: metadb.PermissionFactChannel, HashSlot: route.Route.HashSlot,
				ChannelID: tasks[i].ChannelID, ChannelType: 1,
			}})
			if err != nil || len(facts) != 1 || !facts[0].Found || facts[0].Channel.DirectoryProjectionState != metadb.DirectoryProjectionReady {
				continue
			}
			channel := facts[0].Channel
			expected[i] = channel.DirectoryProjectionGeneration
			valid[i] = tasks[i].Generation == 0 || tasks[i].Generation == expected[i]
		}
	}
	// A racing recovery worker may complete between the read and direct dispatch.
	// Fresh authoritative proof below, not the callback result alone, decides ACK.
	if len(candidates) > 0 {
		prepare(ctx, candidates)
	}
	// Establish the runtime before the coherent Ready+runtime observation. A
	// deletion during allocation invalidates the expected directory generation.
	for i, task := range tasks {
		if valid[i] {
			if _, err := n.ResolveChannelAppendAuthority(ctx, ch.ChannelID{ID: task.ChannelID, Type: 1}); err != nil {
				valid[i] = false
			}
		}
	}
	reads := make([]slotproxy.PermissionMetadataRead, len(tasks))
	for i, task := range tasks {
		reads[i] = slotproxy.PermissionMetadataRead{Kind: slotproxy.PermissionMetadataReadChannel, ChannelID: task.ChannelID, ChannelType: 1}
	}
	facts := n.ReadSendPermissionMetadataBatch(ctx, reads)
	ready := make([]metadb.PersonDirectoryAppendFence, len(tasks))
	if len(facts) == len(tasks) {
		for i, fact := range facts {
			if valid[i] && expected[i] != 0 && fact.Err == nil && fact.Found && fact.Channel.Disband == 0 && fact.Channel.DirectoryProjectionState == metadb.DirectoryProjectionReady && fact.Channel.DirectoryProjectionGeneration == expected[i] && fact.PersonDirectoryFence.Valid() && fact.PersonDirectoryFence.DirectoryGeneration == expected[i] {
				ready[i] = fact.PersonDirectoryFence
			}
		}
	}
	return json.Marshal(personPrepareResponse{Version: 1, Ready: ready})
}
