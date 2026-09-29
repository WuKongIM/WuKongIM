package cluster

import (
	"context"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

// SendPermissionRoutes projects one immutable authority publication without
// copying peer lists. The proxy preserves every distributed fence in its RPC.
func (n *Node) SendPermissionRoutes(keys []string) []proxy.SendPermissionRoute {
	out := make([]proxy.SendPermissionRoute, len(keys))
	routes, err := n.RouteAuthoritiesPartial(keys)
	if err != nil {
		for i := range out {
			out[i].Err = err
		}
		return out
	}
	for i, r := range routes {
		a := r.Authority
		out[i] = proxy.SendPermissionRoute{HashSlot: a.HashSlot, Err: r.Err, Fence: proxy.SendPermissionFence{
			SlotID: uint64(a.SlotID), LeaderNodeID: a.LeaderNodeID, LeaderTerm: a.LeaderTerm, ConfigEpoch: a.ConfigEpoch, RouteRevision: a.RouteRevision,
		}}
	}
	return out
}

// SetSendBanMetadata changes exactly one entity through the Slot apply path.
func (n *Node) SetSendBanMetadata(ctx context.Context, q metadb.SendBanMutation) (metadb.SendBanResult, error) {
	if err := n.ensureForeground(); err != nil {
		return metadb.SendBanResult{}, err
	}
	if n.defaultSlotProxy == nil {
		return metadb.SendBanResult{}, ErrNotStarted
	}
	return n.defaultSlotProxy.ApplySendBan(ctx, q)
}

// ReadSendPermissionMetadataBatch uses fresh Slot barriers and node envelopes.
func (n *Node) ReadSendPermissionMetadataBatch(ctx context.Context, q []proxy.PermissionMetadataRead) []proxy.PermissionMetadataReadResult {
	if err := n.ensureForeground(); err != nil {
		out := make([]proxy.PermissionMetadataReadResult, len(q))
		for i := range out {
			out[i].Err = err
		}
		return out
	}
	if n.defaultSlotProxy == nil {
		out := make([]proxy.PermissionMetadataReadResult, len(q))
		for i := range out {
			out[i].Err = ErrNotStarted
		}
		return out
	}
	return n.defaultSlotProxy.ReadSendPermissionMetadataBatch(ctx, q)
}

// UpdateChannelInfoMetadata applies business flags without overwriting policy.
func (n *Node) UpdateChannelInfoMetadata(ctx context.Context, q metadb.ChannelInfoMutation) (metadb.SendBanResult, error) {
	if err := n.ensureForeground(); err != nil {
		return metadb.SendBanResult{}, err
	}
	if n.defaultSlotProxy == nil {
		return metadb.SendBanResult{}, ErrNotStarted
	}
	return n.defaultSlotProxy.UpdateChannelInfo(ctx, q)
}

// AcquireSendPermissionRead holds maintenance admission through the barrier,
// pinned snapshot and final fence validation, so restore cannot cut between them.
func (n *Node) AcquireSendPermissionRead(ctx context.Context) (func(), error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	return n.acquireWriteAdmission()
}
