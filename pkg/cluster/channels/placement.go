package channels

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"sort"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
)

// SlotPlacementResolver derives initial channel placement from Slot-routed data nodes.
type SlotPlacementResolver struct {
	router       PlacementRouter
	dataNodes    DataNodeProvider
	replicaCount int
}

// NewSlotPlacementResolver creates a placement resolver backed by Slot routing and data-node candidates.
func NewSlotPlacementResolver(router PlacementRouter, dataNodes DataNodeProvider, replicaCount int) *SlotPlacementResolver {
	return &SlotPlacementResolver{router: router, dataNodes: dataNodes, replicaCount: replicaCount}
}

// ResolveChannelPlacement returns the preferred Channel leader and peers for id.
func (r *SlotPlacementResolver) ResolveChannelPlacement(ctx context.Context, id ch.ChannelID) (ChannelPlacement, error) {
	if err := ctxErr(ctx); err != nil {
		return ChannelPlacement{}, err
	}
	if r == nil || r.router == nil {
		return ChannelPlacement{}, fmt.Errorf("%w: placement router is nil", ch.ErrInvalidConfig)
	}
	if r.dataNodes == nil {
		return ChannelPlacement{}, fmt.Errorf("%w: data node provider is nil", ch.ErrInvalidConfig)
	}
	route, err := r.router.RouteKey(id.ID)
	if err != nil {
		return ChannelPlacement{}, err
	}
	candidates, err := r.dataNodes.PlacementDataNodes(ctx, route.Revision)
	if err != nil {
		return ChannelPlacement{}, err
	}
	placements, err := r.resolveChannelPlacements([]ch.ChannelID{id}, []routing.Route{route}, candidates)
	if err != nil {
		return ChannelPlacement{}, err
	}
	return placements[0], nil
}

// ResolveChannelPlacementBatch derives every placement in one submitted batch
// from one current data-node snapshot and the batch's revalidated Slot routes.
func (r *SlotPlacementResolver) ResolveChannelPlacementBatch(ctx context.Context, ids []ch.ChannelID, routes []routing.Route) ([]ChannelPlacement, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if r == nil || r.dataNodes == nil {
		return nil, fmt.Errorf("%w: data node provider is nil", ch.ErrInvalidConfig)
	}
	if len(ids) != len(routes) {
		return nil, fmt.Errorf("%w: aligned channel placement routes", ch.ErrInvalidConfig)
	}
	if len(routes) == 0 {
		return []ChannelPlacement{}, nil
	}
	expectedRevision := routes[0].Revision
	for _, route := range routes[1:] {
		if route.Revision != expectedRevision {
			return nil, fmt.Errorf("%w: mixed channel placement route revisions", ch.ErrStaleMeta)
		}
	}
	candidates, err := r.dataNodes.PlacementDataNodes(ctx, expectedRevision)
	if err != nil {
		return nil, err
	}
	return r.resolveChannelPlacements(ids, routes, candidates)
}

func (r *SlotPlacementResolver) resolveChannelPlacements(ids []ch.ChannelID, routes []routing.Route, candidates []uint64) ([]ChannelPlacement, error) {
	if len(ids) != len(routes) {
		return nil, fmt.Errorf("%w: aligned channel placement routes", ch.ErrInvalidConfig)
	}
	placements := make([]ChannelPlacement, len(ids))
	for i, id := range ids {
		selected, err := selectChannelReplicas(string(ch.ChannelKeyForID(id)), candidates, r.replicaCount)
		if err != nil {
			return nil, err
		}
		replicas := make([]ch.NodeID, 0, len(selected))
		for _, node := range selected {
			replicas = append(replicas, ch.NodeID(node))
		}
		leader := replicas[0]
		if routes[i].PreferredLeader != 0 && uint64NodeIn(selected, routes[i].PreferredLeader) {
			leader = ch.NodeID(routes[i].PreferredLeader)
		}
		placements[i] = ChannelPlacement{Leader: leader, Replicas: replicas, MinISR: len(replicas)/2 + 1}
	}
	return placements, nil
}

type scoredNode struct {
	node  uint64
	score uint64
}

func selectChannelReplicas(channelID string, candidates []uint64, replicaCount int) ([]uint64, error) {
	if replicaCount <= 0 {
		return nil, fmt.Errorf("%w: channel replica count must be positive", ch.ErrInvalidConfig)
	}
	uniq := uniqueSortedUint64(candidates)
	if len(uniq) < replicaCount {
		return nil, fmt.Errorf("%w: channel replica candidates %d below replica count %d", ch.ErrInvalidConfig, len(uniq), replicaCount)
	}
	scored := make([]scoredNode, 0, len(uniq))
	for _, node := range uniq {
		scored = append(scored, scoredNode{node: node, score: rendezvousScore(channelID, node)})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].node < scored[j].node
		}
		return scored[i].score > scored[j].score
	})
	out := make([]uint64, 0, replicaCount)
	for i := 0; i < replicaCount; i++ {
		out = append(out, scored[i].node)
	}
	return out, nil
}

func rendezvousScore(channelID string, node uint64) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(channelID))
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], node)
	_, _ = h.Write(buf[:])
	return h.Sum64()
}

func uniqueSortedUint64(values []uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}
	out := append([]uint64(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	n := 0
	for _, value := range out {
		if n == 0 || out[n-1] != value {
			out[n] = value
			n++
		}
	}
	return out[:n]
}

func uint64NodeIn(nodes []uint64, node uint64) bool {
	for _, item := range nodes {
		if item == node {
			return true
		}
	}
	return false
}
