package proxy

import (
	"context"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

// AdjustMQTTStorage retains the exact durable reply, including capacity
// refusals and current grant evidence after an uncertain older allocation.
func (s *Store) AdjustMQTTStorage(ctx context.Context, q metadb.MQTTStorageAdjustment) (metadb.MQTTStorageResult, error) {
	var out metadb.MQTTStorageResult
	cmd, err := metafsm.EncodeMQTTStorageCommand(q)
	if err != nil {
		return out, err
	}
	if err = s.proposeMQTT(ctx, metadb.MQTTStorageRoutingKey, cmd, &out); err != nil {
		return metadb.MQTTStorageResult{}, err
	}
	if out.Grant.NodeID != q.NodeID || out.Limit == 0 || out.Total > out.Limit || out.Grant.Bytes > out.Total {
		return metadb.MQTTStorageResult{}, metadb.ErrCorruptValue
	}
	switch out.Status {
	case "applied", "unchanged":
		if out.Grant.Revision != q.ExpectedRevision+1 || out.Grant.Bytes != q.TargetBytes || !out.MembersMatch {
			return metadb.MQTTStorageResult{}, metadb.ErrCorruptValue
		}
	case "conflict", "full", "config_mismatch":
	default:
		return metadb.MQTTStorageResult{}, metadb.ErrCorruptValue
	}
	return out, nil
}
