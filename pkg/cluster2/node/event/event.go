package event

import "github.com/WuKongIM/WuKongIM/pkg/cluster2/node/types"

type IEvent interface {

	// OnSlotElection 槽选举
	OnSlotElection(slots []*types.Slot) error
}
