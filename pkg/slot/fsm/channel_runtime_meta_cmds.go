package fsm

import (
	"bytes"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var createChannelRuntimeMetaResultMagic = [...]byte{'W', 'K', 'R', 'M', 1}

// CreateChannelRuntimeMetaResult reports whether the authoritative apply inserted the row.
type CreateChannelRuntimeMetaResult struct {
	Created bool `json:"created"`
}

type createChannelRuntimeMetaCmd struct {
	meta   metadb.ChannelRuntimeMeta
	result *metadb.ChannelRuntimeMetaCreateResult
}

func (c *createChannelRuntimeMetaCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	result, err := wb.CreateChannelRuntimeMeta(hashSlot, c.meta)
	c.result = result
	return err
}

func (c *createChannelRuntimeMetaCmd) applyResult() []byte {
	result := CreateChannelRuntimeMetaResult{}
	if c.result != nil {
		result.Created = c.result.Created
	}
	return EncodeCreateChannelRuntimeMetaResult(result)
}

// EncodeCreateChannelRuntimeMetaCommand encodes a create-only runtime metadata command.
func EncodeCreateChannelRuntimeMetaCommand(meta metadb.ChannelRuntimeMeta) []byte {
	data := EncodeUpsertChannelRuntimeMetaCommand(meta)
	data[1] = cmdTypeCreateChannelRuntimeMeta
	return data
}

func decodeCreateChannelRuntimeMeta(data []byte) (command, error) {
	decoded, err := decodeUpsertChannelRuntimeMeta(data)
	if err != nil {
		return nil, err
	}
	upsert, ok := decoded.(*upsertChannelRuntimeMetaCmd)
	if !ok {
		return nil, fmt.Errorf("%w: create runtime metadata payload", metadb.ErrCorruptValue)
	}
	return &createChannelRuntimeMetaCmd{meta: upsert.meta}, nil
}

// EncodeCreateChannelRuntimeMetaResult encodes the authoritative create outcome.
func EncodeCreateChannelRuntimeMetaResult(result CreateChannelRuntimeMetaResult) []byte {
	created := byte(0)
	if result.Created {
		created = 1
	}
	return append(append([]byte(nil), createChannelRuntimeMetaResultMagic[:]...), created)
}

// DecodeCreateChannelRuntimeMetaResult decodes the authoritative create outcome.
func DecodeCreateChannelRuntimeMetaResult(data []byte) (CreateChannelRuntimeMetaResult, error) {
	if len(data) != len(createChannelRuntimeMetaResultMagic)+1 ||
		!bytes.HasPrefix(data, createChannelRuntimeMetaResultMagic[:]) {
		return CreateChannelRuntimeMetaResult{}, fmt.Errorf("%w: create channel runtime metadata result", metadb.ErrCorruptValue)
	}
	switch data[len(createChannelRuntimeMetaResultMagic)] {
	case 0:
		return CreateChannelRuntimeMetaResult{}, nil
	case 1:
		return CreateChannelRuntimeMetaResult{Created: true}, nil
	default:
		return CreateChannelRuntimeMetaResult{}, fmt.Errorf("%w: create channel runtime metadata created value", metadb.ErrCorruptValue)
	}
}
