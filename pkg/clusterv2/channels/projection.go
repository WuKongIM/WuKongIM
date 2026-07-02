package channels

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func cloneMeta(meta ch.Meta) ch.Meta {
	meta.Replicas = append([]ch.NodeID(nil), meta.Replicas...)
	meta.ISR = append([]ch.NodeID(nil), meta.ISR...)
	return meta
}

// ProjectRuntimeMeta converts Slot-owned runtime metadata into the ChannelV2 runtime view.
func ProjectRuntimeMeta(meta metadb.ChannelRuntimeMeta) ch.Meta {
	meta = metadb.NormalizeChannelRuntimeMeta(meta)
	id := ch.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)}
	var leaseUntil time.Time
	if meta.LeaseUntilMS > 0 {
		leaseUntil = time.UnixMilli(meta.LeaseUntilMS).UTC()
	}
	var writeFence ch.WriteFence
	if meta.WriteFenceToken != "" || meta.WriteFenceVersion != 0 || meta.WriteFenceReason != 0 || meta.WriteFenceUntilMS != 0 {
		writeFence = ch.WriteFence{
			Token:   meta.WriteFenceToken,
			Version: meta.WriteFenceVersion,
			Reason:  ch.WriteFenceReason(meta.WriteFenceReason),
		}
		if meta.WriteFenceUntilMS > 0 {
			writeFence.Until = time.UnixMilli(meta.WriteFenceUntilMS).UTC()
		}
	}
	return ch.Meta{
		Key:                 ch.ChannelKeyForID(id),
		ID:                  id,
		Epoch:               meta.ChannelEpoch,
		LeaderEpoch:         meta.LeaderEpoch,
		Leader:              ch.NodeID(meta.Leader),
		Replicas:            projectNodeIDs(meta.Replicas),
		ISR:                 projectNodeIDs(meta.ISR),
		MinISR:              int(meta.MinISR),
		LeaseUntil:          leaseUntil,
		RetentionThroughSeq: meta.RetentionThroughSeq,
		WriteFence:          writeFence,
		Status:              ch.Status(meta.Status),
	}
}

func projectRuntimeMeta(meta metadb.ChannelRuntimeMeta) ch.Meta {
	return ProjectRuntimeMeta(meta)
}

func projectNodeIDs(ids []uint64) []ch.NodeID {
	out := make([]ch.NodeID, 0, len(ids))
	for _, id := range ids {
		out = append(out, ch.NodeID(id))
	}
	return out
}

func projectUint64NodeIDs(ids []ch.NodeID) []uint64 {
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		out = append(out, uint64(id))
	}
	return out
}
