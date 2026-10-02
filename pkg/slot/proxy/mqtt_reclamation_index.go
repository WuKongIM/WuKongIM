package proxy

import (
	"context"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

// BuildMQTTReclamationIndex advances one bounded backfill page at the selected
// logical hash Slot's current authority. It never rebuilds a convenient replica.
func (s *Store) BuildMQTTReclamationIndex(ctx context.Context, hashSlot uint16) (out metadb.MQTTReclamationIndexResult, err error) {
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if s == nil || s.cluster == nil {
		return out, errSlotNotFound
	}
	proposer, ok := s.cluster.(hashSlotResultProposer)
	if !ok {
		return out, fmt.Errorf("metastore: MQTT requires committed proposal results")
	}
	revision := s.cluster.HashSlotTableVersion()
	slot, err := s.mqttHashSlotOwner(hashSlot)
	if err != nil {
		return out, err
	}
	if revision != s.cluster.HashSlotTableVersion() {
		return out, metadb.ErrStaleMeta
	}
	body, err := proposer.ProposeWithHashSlotResult(ctx, slot, hashSlot, metafsm.EncodeMQTTReclamationIndexCommand())
	if err != nil {
		return out, err
	}
	if string(body) == metafsm.ApplyResultHashSlotFenced || string(body) == metafsm.ApplyResultStaleMeta {
		return out, metadb.ErrStaleMeta
	}
	if decodeMQTTJSON(body, 4096, &out) != nil || out.Scanned < 0 || out.Scanned > 64 || !out.Done && out.Scanned != 64 {
		return metadb.MQTTReclamationIndexResult{}, metadb.ErrCorruptValue
	}
	return out, nil
}
