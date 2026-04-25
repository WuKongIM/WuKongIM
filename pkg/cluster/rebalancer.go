package cluster

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster/hashslot"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type MigrationPlan = hashslot.MigrationPlan

func ComputeAddSlotPlan(table *HashSlotTable, newSlotID multiraft.SlotID) []MigrationPlan {
	return hashslot.ComputeAddSlotPlan(table, newSlotID)
}

func ComputeRemoveSlotPlan(table *HashSlotTable, removeSlotID multiraft.SlotID) []MigrationPlan {
	return hashslot.ComputeRemoveSlotPlan(table, removeSlotID)
}

func ComputeRebalancePlan(table *HashSlotTable) []MigrationPlan {
	return hashslot.ComputeRebalancePlan(table)
}
