package proxy

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type hashSlotKeyer interface {
	HashSlotForKey(string) uint16
}

type hashSlotProposer interface {
	ProposeWithHashSlot(context.Context, multiraft.SlotID, uint16, []byte) error
}

type localHashSlotProposer interface {
	ProposeLocalWithHashSlot(context.Context, multiraft.SlotID, uint16, []byte) error
}

// hashSlotForKey prefers the cluster-provided hash-slot lookup when available.
// Returning zero without a dedicated API is safer than guessing from physical
// slot routing, which can point at the wrong hash slot once a slot owns
// multiple logical partitions.
func hashSlotForKey(cluster any, key string) uint16 {
	if cluster == nil {
		return 0
	}
	if keyer, ok := cluster.(hashSlotKeyer); ok {
		return keyer.HashSlotForKey(key)
	}
	return 0
}

// proposeWithHashSlot uses ProposeWithHashSlot when the cluster exposes it.
// Older in-flight builds can still compile and run by falling back to Propose.
func proposeWithHashSlot(ctx context.Context, cluster any, slotID multiraft.SlotID, hashSlot uint16, cmd []byte) error {
	if cluster == nil {
		return fmt.Errorf("metastore: cluster not configured")
	}
	if proposer, ok := cluster.(hashSlotProposer); ok {
		return proposer.ProposeWithHashSlot(ctx, slotID, hashSlot, cmd)
	}
	if proposer, ok := cluster.(interface {
		Propose(context.Context, multiraft.SlotID, []byte) error
	}); ok {
		return proposer.Propose(ctx, slotID, cmd)
	}
	return fmt.Errorf("metastore: cluster does not support proposal submission")
}

func proposeLocalWithHashSlot(ctx context.Context, cluster any, slotID multiraft.SlotID, hashSlot uint16, cmd []byte) error {
	if cluster == nil {
		return fmt.Errorf("metastore: cluster not configured")
	}
	if proposer, ok := cluster.(localHashSlotProposer); ok {
		return proposer.ProposeLocalWithHashSlot(ctx, slotID, hashSlot, cmd)
	}
	return fmt.Errorf("metastore: cluster does not support local proposal submission")
}
