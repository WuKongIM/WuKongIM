package event

import "github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"

type IEvent interface {

	// OnSlotElection 槽选举
	OnSlotElection(slots []*types.Slot) error
}
