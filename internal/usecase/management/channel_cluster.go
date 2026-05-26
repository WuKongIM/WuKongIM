package management

import (
	"context"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	channelClusterScanPageLimit = 200

	// ChannelClusterUnhealthyReasonISRInsufficient means ISR is below MinISR.
	ChannelClusterUnhealthyReasonISRInsufficient = "isr_insufficient"
	// ChannelClusterUnhealthyReasonNoLeader means the channel has no current leader.
	ChannelClusterUnhealthyReasonNoLeader = "no_leader"
	// ChannelClusterUnhealthyReasonStatusNotActive means the channel is not active.
	ChannelClusterUnhealthyReasonStatusNotActive = "status_not_active"
)

// ChannelClusterSummary aggregates manager-facing health counters for channel ISR groups.
type ChannelClusterSummary struct {
	// Total is the number of channel runtime records scanned across all physical slots.
	Total int
	// Healthy counts active channels with a leader and enough in-sync replicas.
	Healthy int
	// ISRInsufficient counts channels whose in-sync replica count is below MinISR.
	ISRInsufficient int
	// NoLeader counts channels with no current leader.
	NoLeader int
	// AvgReplicas is the average configured replica count across scanned channels.
	AvgReplicas float64
	// AvgISR is the average in-sync replica count across scanned channels.
	AvgISR float64
	// LeaderDistribution counts channels led by each non-zero node ID.
	LeaderDistribution []ChannelLeaderDistribution
}

// ChannelLeaderDistribution counts channel leaders assigned to one node.
type ChannelLeaderDistribution struct {
	// NodeID is the channel leader node ID.
	NodeID uint64
	// Count is the number of scanned channels led by NodeID.
	Count int
}

// ChannelClusterUnhealthyItem is a channel runtime row with stable health reasons.
type ChannelClusterUnhealthyItem struct {
	ChannelRuntimeMeta
	// Reasons describes why the channel is considered unhealthy.
	Reasons []string
}

// ListChannelClusterUnhealthyRequest configures an unhealthy channel page.
type ListChannelClusterUnhealthyRequest struct {
	// Limit is the maximum unhealthy items to return.
	Limit int
	// Cursor resumes scanning from a previous response.
	Cursor ChannelRuntimeMetaListCursor
}

// ListChannelClusterUnhealthyResponse returns one unhealthy channel page.
type ListChannelClusterUnhealthyResponse struct {
	// Items contains unhealthy channel rows.
	Items []ChannelClusterUnhealthyItem
	// HasMore reports whether another page exists.
	HasMore bool
	// NextCursor identifies the next scan position.
	NextCursor ChannelRuntimeMetaListCursor
}

// GetChannelClusterSummary scans channel runtime metadata and aggregates ISR health.
func (a *App) GetChannelClusterSummary(ctx context.Context) (ChannelClusterSummary, error) {
	if a == nil || a.cluster == nil || a.channelRuntimeMeta == nil {
		return ChannelClusterSummary{}, nil
	}

	slotIDs := sortedChannelClusterSlotIDs(a.cluster.SlotIDs())
	summary := ChannelClusterSummary{}
	leaderCounts := make(map[uint64]int)
	var replicaTotal int
	var isrTotal int

	for _, slotID := range slotIDs {
		after := metadb.ChannelRuntimeMetaCursor{}
		for {
			page, nextCursor, done, err := a.channelRuntimeMeta.ScanChannelRuntimeMetaSlotPage(ctx, slotID, after, channelClusterScanPageLimit)
			if err != nil {
				return ChannelClusterSummary{}, err
			}

			for _, meta := range page {
				summary.Total++
				replicaTotal += len(meta.Replicas)
				isrTotal += len(meta.ISR)
				if meta.Leader != 0 {
					leaderCounts[meta.Leader]++
				}
				if channelClusterMetaHealthy(meta) {
					summary.Healthy++
				}
				if channelClusterMetaISRInsufficient(meta) {
					summary.ISRInsufficient++
				}
				if meta.Leader == 0 {
					summary.NoLeader++
				}
			}

			if done {
				break
			}
			after = nextCursor
		}
	}

	if summary.Total > 0 {
		summary.AvgReplicas = float64(replicaTotal) / float64(summary.Total)
		summary.AvgISR = float64(isrTotal) / float64(summary.Total)
	}
	summary.LeaderDistribution = channelLeaderDistribution(leaderCounts)
	return summary, nil
}

// ListChannelClusterUnhealthy returns channels with ISR, leader, or status health reasons.
func (a *App) ListChannelClusterUnhealthy(ctx context.Context, req ListChannelClusterUnhealthyRequest) (ListChannelClusterUnhealthyResponse, error) {
	if a == nil || a.cluster == nil || a.channelRuntimeMeta == nil {
		return ListChannelClusterUnhealthyResponse{}, nil
	}
	if req.Limit <= 0 {
		return ListChannelClusterUnhealthyResponse{}, metadb.ErrInvalidArgument
	}
	if err := validateChannelRuntimeMetaListCursor(req.Cursor); err != nil {
		return ListChannelClusterUnhealthyResponse{}, err
	}

	slotIDs := sortedChannelClusterSlotIDs(a.cluster.SlotIDs())
	startIndex, err := channelRuntimeMetaStartSlotIndex(slotIDs, req.Cursor.SlotID)
	if err != nil {
		return ListChannelClusterUnhealthyResponse{}, err
	}

	resp := ListChannelClusterUnhealthyResponse{
		Items: make([]ChannelClusterUnhealthyItem, 0, req.Limit),
	}
	var lastEmitted ChannelRuntimeMeta
	for i := startIndex; i < len(slotIDs); i++ {
		slotID := slotIDs[i]
		after := metadb.ChannelRuntimeMetaCursor{}
		if i == startIndex {
			after = req.Cursor.shardCursor()
		}

		for {
			page, nextCursor, done, err := a.channelRuntimeMeta.ScanChannelRuntimeMetaSlotPage(ctx, slotID, after, channelClusterScanPageLimit)
			if err != nil {
				return ListChannelClusterUnhealthyResponse{}, err
			}
			items, err := a.managerChannelRuntimeMetaItems(ctx, slotID, page, true)
			if err != nil {
				return ListChannelClusterUnhealthyResponse{}, err
			}
			for _, item := range items {
				reasons := channelClusterUnhealthyReasons(item)
				if len(reasons) == 0 {
					continue
				}
				if len(resp.Items) == req.Limit {
					resp.HasMore = true
					resp.NextCursor = channelRuntimeMetaListCursorForItem(lastEmitted)
					return resp, nil
				}
				resp.Items = append(resp.Items, ChannelClusterUnhealthyItem{
					ChannelRuntimeMeta: item,
					Reasons:            reasons,
				})
				lastEmitted = item
			}
			if done {
				break
			}
			after = nextCursor
		}
	}
	return resp, nil
}

func sortedChannelClusterSlotIDs(slotIDs []multiraft.SlotID) []multiraft.SlotID {
	out := append([]multiraft.SlotID(nil), slotIDs...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func channelClusterMetaHealthy(meta metadb.ChannelRuntimeMeta) bool {
	return channel.Status(meta.Status) == channel.StatusActive &&
		meta.Leader != 0 &&
		!channelClusterMetaISRInsufficient(meta)
}

func channelClusterMetaISRInsufficient(meta metadb.ChannelRuntimeMeta) bool {
	return int64(len(meta.ISR)) < meta.MinISR
}

func channelLeaderDistribution(counts map[uint64]int) []ChannelLeaderDistribution {
	nodeIDs := make([]uint64, 0, len(counts))
	for nodeID := range counts {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })

	out := make([]ChannelLeaderDistribution, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		out = append(out, ChannelLeaderDistribution{
			NodeID: nodeID,
			Count:  counts[nodeID],
		})
	}
	return out
}

func channelClusterUnhealthyReasons(item ChannelRuntimeMeta) []string {
	reasons := make([]string, 0, 3)
	if int64(len(item.ISR)) < item.MinISR {
		reasons = append(reasons, ChannelClusterUnhealthyReasonISRInsufficient)
	}
	if item.Leader == 0 {
		reasons = append(reasons, ChannelClusterUnhealthyReasonNoLeader)
	}
	if item.Status != "active" {
		reasons = append(reasons, ChannelClusterUnhealthyReasonStatusNotActive)
	}
	return reasons
}
