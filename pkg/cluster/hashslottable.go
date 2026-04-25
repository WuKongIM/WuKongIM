package cluster

import "github.com/WuKongIM/WuKongIM/pkg/cluster/hashslot"

type HashSlotTable = hashslot.HashSlotTable
type HashSlotMigration = hashslot.HashSlotMigration
type MigrationPhase = hashslot.MigrationPhase

const (
	PhaseSnapshot  = hashslot.PhaseSnapshot
	PhaseDelta     = hashslot.PhaseDelta
	PhaseSwitching = hashslot.PhaseSwitching
	PhaseDone      = hashslot.PhaseDone
)

func NewHashSlotTable(hashSlotCount uint16, physicalSlotCount int) *HashSlotTable {
	return hashslot.NewHashSlotTable(hashSlotCount, physicalSlotCount)
}

func DecodeHashSlotTable(data []byte) (*HashSlotTable, error) {
	return hashslot.DecodeHashSlotTable(data)
}

func HashSlotForKey(key string, hashSlotCount uint16) uint16 {
	return hashslot.HashSlotForKey(key, hashSlotCount)
}
