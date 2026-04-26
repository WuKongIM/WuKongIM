package replica

import "github.com/WuKongIM/WuKongIM/pkg/channel"

type replicaMachineState struct {
	meta               channel.Meta
	replica            channel.ReplicaState
	progress           map[channel.NodeID]uint64
	epochHistory       []channel.EpochPoint
	roleGeneration     uint64
	nextEffectID       uint64
	checkpointEffectID uint64
	closed             bool
}

func (s replicaMachineState) clone() replicaMachineState {
	out := s
	out.meta.Replicas = append([]channel.NodeID(nil), s.meta.Replicas...)
	out.meta.ISR = append([]channel.NodeID(nil), s.meta.ISR...)
	if s.progress != nil {
		out.progress = make(map[channel.NodeID]uint64, len(s.progress))
		for id, offset := range s.progress {
			out.progress[id] = offset
		}
	}
	out.epochHistory = append([]channel.EpochPoint(nil), s.epochHistory...)
	return out
}
