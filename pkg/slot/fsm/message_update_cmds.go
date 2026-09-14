package fsm

import (
	"encoding/json"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const cmdTypeMessageUpdate uint8 = 66

type messageUpdateCmd struct {
	mutation metadb.MessageUpdateMutation
	result   *metadb.MessageUpdateMutationResult
}

func (c *messageUpdateCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	c.result, err = wb.ApplyMessageUpdate(hashSlot, c.mutation)
	return err
}
func (c *messageUpdateCmd) applyResult() []byte { b, _ := json.Marshal(c.result); return b }

// EncodeMessageUpdateCommand encodes a bounded, versioned edit mutation.
func EncodeMessageUpdateCommand(q metadb.MessageUpdateMutation) ([]byte, error) {
	if err := metadb.ValidateMessageUpdateMutation(q); err != nil {
		return nil, err
	}
	body, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}
	return append([]byte{commandVersion, cmdTypeMessageUpdate}, body...), nil
}
func decodeMessageUpdateCommand(data []byte) (command, error) {
	if len(data) > 2*metadb.MaxMessageUpdatePayload {
		return nil, metadb.ErrInvalidArgument
	}
	var q metadb.MessageUpdateMutation
	if err := json.Unmarshal(data, &q); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	if err := metadb.ValidateMessageUpdateMutation(q); err != nil {
		return nil, err
	}
	return &messageUpdateCmd{mutation: q}, nil
}

// MessageUpdateCommandEpoch identifies edit commands without inspecting other
// command payloads. The admission adapter checks this epoch before Raft enqueue.
func MessageUpdateCommandEpoch(data []byte) (uint64, bool, error) {
	if len(data) < 2 || data[0] != commandVersion || data[1] != cmdTypeMessageUpdate {
		return 0, false, nil
	}
	cmd, err := decodeMessageUpdateCommand(data[2:])
	if err != nil {
		return 0, true, err
	}
	return cmd.(*messageUpdateCmd).mutation.ExpectedContentEpoch, true, nil
}
