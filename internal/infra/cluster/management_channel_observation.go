package cluster

import (
	"context"
	"sync"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	management "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

// ManagementChannelObservationNode exposes only non-activating local reads and RPC.
type ManagementChannelObservationNode interface {
	ManagementChannelNode
	ObserveLocalChannels(context.Context, []channels.RuntimeObservationTarget, bool) ([]channels.RuntimeObservation, error)
}

// ManagementChannelObserver groups observations by exact leader with bounded fanout.
type ManagementChannelObserver struct {
	node   ManagementChannelObservationNode
	remote *accessnode.Client
}

// NewManagementChannelObserver creates the routed and node-local observation adapter.
func NewManagementChannelObserver(node ManagementChannelObservationNode) *ManagementChannelObserver {
	return &ManagementChannelObserver{node: node, remote: accessnode.NewClient(node)}
}

// ObserveChannels keeps failed leader groups unknown and preserves request alignment.
func (r *ManagementChannelObserver) ObserveChannels(parent context.Context, targets []management.ChannelObservationTarget, include bool) ([]management.ChannelObservation, error) {
	if r == nil || r.node == nil || len(targets) > channels.MaxRuntimeObservations {
		return nil, management.ErrBusinessChannelReaderUnavailable
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	out := make([]management.ChannelObservation, len(targets))
	groups := make(map[uint64][]int)
	for i, target := range targets {
		out[i].Target = target
		if validChannelObservationTarget(target) {
			groups[target.LeaderID] = append(groups[target.LeaderID], i)
		}
	}
	limit := make(chan struct{}, 4)
	var wg sync.WaitGroup
launch:
	for leader, indexes := range groups {
		select {
		case limit <- struct{}{}:
		case <-ctx.Done():
			break launch
		}
		leader, indexes := leader, indexes
		wg.Add(1)
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskManagerDiagnosticsFanout, func() {
			defer wg.Done()
			defer func() { <-limit }()
			batch := make([]management.ChannelObservationTarget, len(indexes))
			for i, index := range indexes {
				batch[i] = targets[index]
			}
			var rows []management.ChannelObservation
			var err error
			if leader == r.node.NodeID() {
				rows, err = r.ObserveLocalChannels(ctx, batch, include)
			} else {
				rows, err = r.remote.ObserveManagerChannels(ctx, leader, batch, include)
			}
			if err != nil || !validChannelObservationRows(batch, rows, include) {
				return
			}
			for i, index := range indexes {
				out[index] = rows[i]
			}
		})
	}
	wg.Wait()
	if err := parent.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ObserveLocalChannels translates one leader-local batch without routing or activation.
func (r *ManagementChannelObserver) ObserveLocalChannels(ctx context.Context, targets []management.ChannelObservationTarget, include bool) ([]management.ChannelObservation, error) {
	if r == nil || r.node == nil || len(targets) > channels.MaxRuntimeObservations {
		return nil, management.ErrBusinessChannelReaderUnavailable
	}
	batch := make([]channels.RuntimeObservationTarget, len(targets))
	for i, target := range targets {
		if !validChannelObservationTarget(target) || target.LeaderID != r.node.NodeID() {
			return nil, management.ErrBusinessChannelReaderUnavailable
		}
		batch[i] = channels.RuntimeObservationTarget{ChannelID: ch.ChannelID{ID: target.ChannelID, Type: uint8(target.ChannelType)}, LeaderID: ch.NodeID(target.LeaderID), Epoch: target.Epoch, LeaderEpoch: target.LeaderEpoch, RouteGeneration: target.RouteGeneration}
	}
	rows, err := r.node.ObserveLocalChannels(ctx, batch, include)
	if err != nil {
		return nil, err
	}
	if len(rows) != len(targets) {
		return nil, management.ErrBusinessChannelReaderUnavailable
	}
	out := make([]management.ChannelObservation, len(rows))
	for i, row := range rows {
		if row.Target != batch[i] {
			return nil, management.ErrBusinessChannelReaderUnavailable
		}
		out[i] = management.ChannelObservation{Target: targets[i], Known: row.Known, Active: row.Active}
		if row.MaxMessageSeq != nil {
			value := *row.MaxMessageSeq
			out[i].MaxMessageSeq = &value
		}
	}
	if !validChannelObservationRows(targets, out, include) {
		return nil, management.ErrBusinessChannelReaderUnavailable
	}
	return out, nil
}

func validChannelObservationTarget(target management.ChannelObservationTarget) bool {
	return target.ChannelID != "" && len(target.ChannelID) <= 4096 && target.ChannelType > 0 && target.ChannelType <= 255 && target.LeaderID != 0
}

func validChannelObservationRows(targets []management.ChannelObservationTarget, rows []management.ChannelObservation, include bool) bool {
	if len(targets) != len(rows) {
		return false
	}
	for i, row := range rows {
		if row.Target != targets[i] || (!row.Known && (row.Active || row.MaxMessageSeq != nil)) || (!include && row.MaxMessageSeq != nil) {
			return false
		}
	}
	return true
}
