package cluster

import (
	"fmt"

	replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"
)

type Slot struct {
	rc      *replica.Replica
	slotId  uint32
	shardNo string
}

func NewSlot(slotId uint32, appliedIdx uint64, replicas []uint64, opts *Options) *Slot {
	shardNo := GetSlotShardNo(slotId)
	rc := replica.New(opts.NodeID, shardNo, replica.WithAppliedIndex(appliedIdx), replica.WithReplicas(replicas), replica.WithStorage(newProxyReplicaStorage(shardNo, opts.ShardLogStorage)))
	return &Slot{
		slotId:  slotId,
		shardNo: shardNo,
		rc:      rc,
	}
}

func GetSlotShardNo(slotID uint32) string {
	return fmt.Sprintf("slot-%d", slotID)
}
