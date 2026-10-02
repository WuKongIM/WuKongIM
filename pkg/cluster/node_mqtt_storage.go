package cluster

import (
	"context"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// adjustMQTTStorage converts one physical node's exact escrow request to the
// existing foreground-gated authoritative Slot proposal path.
func (n *Node) adjustMQTTStorage(ctx context.Context, q messagedb.MQTTStorageGrantRequest) (messagedb.MQTTStorageGrantReply, error) {
	s, err := n.mqttMetadataStore()
	if err != nil {
		return messagedb.MQTTStorageGrantReply{}, err
	}
	r, err := s.AdjustMQTTStorage(ctx, metadb.MQTTStorageAdjustment{
		NodeID: n.cfg.NodeID, ExpectedRevision: q.Revision, ExpectedBytes: q.Bytes,
		TargetBytes: q.Target, Members: q.Members, MembershipRevision: q.MembershipRevision, InitialDebt: q.InitialDebt, ClusterLimit: n.cfg.Storage.MQTTClusterBytes,
	})
	if err != nil {
		return messagedb.MQTTStorageGrantReply{}, err
	}
	return messagedb.MQTTStorageGrantReply{Revision: r.Grant.Revision, Bytes: r.Grant.Bytes, Limit: r.Limit, Total: r.Total, Status: r.Status, RosterRevision: r.RosterRevision, Ready: r.Ready, MembersMatch: r.MembersMatch, Debt: r.Grant.Debt, Initialized: r.Grant.Initialized}, nil
}

// mqttStorageMembers includes joining/draining nodes. Health expiry cannot remove debt.
func (n *Node) mqttStorageMembers() ([]uint64, uint64) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.mqttStorageMemberIDs, n.controlSnapshot.Revision
}
