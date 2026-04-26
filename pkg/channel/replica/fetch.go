package replica

import "github.com/WuKongIM/WuKongIM/pkg/channel"

func visibleCommittedHW(state channel.ReplicaState) uint64 {
	if state.CommitReady {
		return state.HW
	}
	if state.CheckpointHW < state.HW {
		return state.CheckpointHW
	}
	return state.HW
}
