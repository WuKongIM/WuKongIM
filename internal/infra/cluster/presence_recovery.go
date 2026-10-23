package cluster

import (
	"context"
	"errors"
	"sort"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// PresenceRecoveryNode supplies exact routing and current membership evidence.
type PresenceRecoveryNode interface {
	PresenceNode
	LocalControllerSnapshot(context.Context) (control.Snapshot, error)
}

// PresenceRecoveryOwners reads indexed owner projections only during UID
// reconstruction. One node-wide gate bounds network reads across all hash slots.
type PresenceRecoveryOwners struct {
	node      PresenceRecoveryNode
	local     presence.OwnerRouteReader
	remote    *accessnode.Client
	admission chan struct{}
}

// NewPresenceRecoveryOwners shares bounded owner-read admission across UID authorities.
func NewPresenceRecoveryOwners(node PresenceRecoveryNode, local presence.OwnerRouteReader) *PresenceRecoveryOwners {
	return &PresenceRecoveryOwners{node: node, local: local, remote: accessnode.NewClient(node), admission: make(chan struct{}, 16)}
}

// ValidatePresenceTarget checks the distributed identity; local diagnostic
// revision/epoch counters are never compared across nodes.
func ValidatePresenceTarget(node PresenceNode, target presence.RouteTarget) error {
	route, err := node.RouteHashSlot(target.HashSlot)
	if err != nil {
		return mapPresenceRouteError(err)
	}
	current := routeTargetFromClusterRoute(route)
	if current.HashSlot != target.HashSlot || current.SlotID != target.SlotID || current.LeaderNodeID != target.LeaderNodeID || current.LeaderTerm != target.LeaderTerm || current.ConfigEpoch != target.ConfigEpoch {
		return authority.ErrNotLeader
	}
	return nil
}

// RecoverRoutes uses the same membership proof as multi-target recovery.
func (r *PresenceRecoveryOwners) RecoverRoutes(ctx context.Context, target presence.RouteTarget, uids []string) ([]presence.Route, error) {
	result := r.RecoverRoutesByTargets(ctx, []presence.EndpointLookupGroup{{Target: target, UIDs: uids}})[0]
	return result.Routes, result.Err
}

// RecoverRoutesByTargets queries each current owner once for a complete bounded
// page. Stale target errors remain aligned; missing owner evidence never becomes
// a successful empty result. One unavailable member costs one timeout per page.
func (r *PresenceRecoveryOwners) RecoverRoutesByTargets(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
	results := make([]presence.EndpointLookupResult, len(groups))
	fail := func(err error) []presence.EndpointLookupResult {
		for i := range results {
			results[i] = presence.EndpointLookupResult{Err: err}
		}
		return results
	}
	if r == nil || r.node == nil || r.local == nil || !presence.ValidOwnerRecoveryPage(groups) {
		return fail(authority.ErrRouteNotReady)
	}
	select {
	case r.admission <- struct{}{}:
	case <-ctx.Done():
		return fail(ctx.Err())
	}
	defer func() { <-r.admission }()
	active := make([]presence.EndpointLookupGroup, 0, len(groups))
	indexes := make([]int, 0, len(groups))
	allowed := make([]map[string]struct{}, 0, len(groups))
	for i, group := range groups {
		if err := ValidatePresenceTarget(r.node, group.Target); err != nil {
			results[i].Err = err
			continue
		}
		active = append(active, group)
		indexes = append(indexes, i)
		uids := make(map[string]struct{}, len(group.UIDs))
		for _, uid := range group.UIDs {
			uids[uid] = struct{}{}
		}
		allowed = append(allowed, uids)
	}
	if len(active) == 0 {
		return results
	}
	before, err := r.node.LocalControllerSnapshot(ctx)
	if err != nil {
		return fail(authority.ErrRouteNotReady)
	}
	nodes := recoveryOwnerNodes(before)
	if len(nodes) == 0 || len(nodes) > 256 {
		return fail(authority.ErrRouteNotReady)
	}
	total := 0
	for _, node := range nodes {
		readCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		snapshots, readErr := r.readOwnerPage(readCtx, node.NodeID, active)
		cancel()
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if readErr != nil {
			// Query stale members too: a reachable ingress may still own sessions.
			// Only unavailable members with expired/down Controller health may be omitted.
			if (node.Health.Freshness == control.NodeHealthStale || node.Health.Status == control.NodeDown) &&
				(errors.Is(readErr, context.DeadlineExceeded) || errors.Is(readErr, transport.ErrTimeout) || errors.Is(readErr, transport.ErrDialFailed) || errors.Is(readErr, transport.ErrNodeNotFound)) {
				continue
			}
			return fail(authority.ErrRouteNotReady)
		}
		if len(snapshots) != len(active) {
			return fail(authority.ErrRouteNotReady)
		}
		for j, read := range snapshots {
			i := indexes[j]
			if results[i].Err != nil {
				continue
			}
			if read.Err != nil {
				// The parent plan is still live (checked above). A child owner
				// timeout must keep the group's normal presence retry path.
				if errors.Is(read.Err, context.DeadlineExceeded) || errors.Is(read.Err, context.Canceled) || errors.Is(read.Err, transport.ErrTimeout) || errors.Is(read.Err, transport.ErrDialFailed) || errors.Is(read.Err, transport.ErrNodeNotFound) {
					read.Err = authority.ErrRouteNotReady
				}
				results[i] = presence.EndpointLookupResult{Err: read.Err}
				continue
			}
			snapshot := read.Snapshot
			if snapshot.OwnerNodeID != node.NodeID || snapshot.OwnerBootID == 0 {
				results[i] = presence.EndpointLookupResult{Err: authority.ErrStaleRoute}
				continue
			}
			valid := true
			for _, route := range snapshot.Routes {
				if _, ok := allowed[j][route.UID]; !ok || route.OwnerNodeID != node.NodeID || route.OwnerBootID != snapshot.OwnerBootID || route.SessionID == 0 {
					valid = false
					break
				}
			}
			if !valid {
				results[i] = presence.EndpointLookupResult{Err: authority.ErrStaleRoute}
				continue
			}
			total += len(snapshot.Routes)
			if total > 4096 {
				results[i] = presence.EndpointLookupResult{Err: authority.ErrRouteNotReady}
				continue
			}
			results[i].Routes = append(results[i].Routes, snapshot.Routes...)
		}
	}
	after, err := r.node.LocalControllerSnapshot(ctx)
	if err != nil || !sameRecoveryOwners(nodes, recoveryOwnerNodes(after)) {
		return fail(authority.ErrRouteNotReady)
	}
	for _, i := range indexes {
		if err := ValidatePresenceTarget(r.node, groups[i].Target); err != nil {
			results[i] = presence.EndpointLookupResult{Err: err}
		}
	}
	return results
}

// readOwnerPage keeps operation 9 for singleton readers and uses operation 10
// for coalesced reads. Unsupported batch peers stay explicitly unavailable.
func (r *PresenceRecoveryOwners) readOwnerPage(ctx context.Context, node uint64, groups []presence.EndpointLookupGroup) ([]presence.OwnerRouteResult, error) {
	if len(groups) == 1 {
		var snapshot presence.OwnerRouteSnapshot
		var err error
		if node == r.node.NodeID() {
			snapshot, err = r.local.ReadOwnerRoutes(ctx, groups[0].Target, groups[0].UIDs)
		} else {
			snapshot, err = r.remote.ReadOwnerRoutes(ctx, node, groups[0].Target, groups[0].UIDs)
		}
		if err != nil {
			return nil, err
		}
		return []presence.OwnerRouteResult{{Snapshot: snapshot}}, nil
	}
	if node == r.node.NodeID() {
		batch, ok := r.local.(presence.OwnerRouteBatchReader)
		if !ok {
			return nil, authority.ErrRouteNotReady
		}
		return batch.ReadOwnerRoutesByTargets(ctx, groups), nil
	}
	return r.remote.ReadOwnerRoutesByTargets(ctx, node, groups)
}

// recoveryOwnerNodes includes every active member: gateway session ownership
// is independent of whether a node hosts Channel data replicas.
func recoveryOwnerNodes(snapshot control.Snapshot) []control.Node {
	nodes := make([]control.Node, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.NodeID == 0 || node.JoinState == control.NodeJoinStateRemoved || node.JoinState == control.NodeJoinStateJoining {
			continue
		}
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	return nodes
}

func sameRecoveryOwners(a, b []control.Node) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].NodeID != b[i].NodeID || a[i].Addr != b[i].Addr || a[i].JoinState != b[i].JoinState || a[i].Health.Freshness != b[i].Health.Freshness || a[i].Health.Status != b[i].Health.Status {
			return false
		}
	}
	return true
}
