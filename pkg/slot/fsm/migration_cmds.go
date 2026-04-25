package fsm

import (
	"encoding/binary"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	cmdTypeApplyDelta uint8 = 20
	cmdTypeEnterFence uint8 = 21

	tagApplyDeltaSourceSlotID uint8 = 1
	tagApplyDeltaSourceIndex  uint8 = 2
	tagApplyDeltaHashSlot     uint8 = 3
	tagApplyDeltaOriginalCmd  uint8 = 4

	tagEnterFenceHashSlot uint8 = 1
)

type applyDeltaCmd struct {
	SourceSlotID multiraft.SlotID
	SourceIndex  uint64
	HashSlot     uint16
	OriginalCmd  []byte
}

func EncodeApplyDeltaCommand(sourceSlotID multiraft.SlotID, sourceIndex uint64, hashSlot uint16, originalCmd []byte) []byte {
	data := []byte{commandVersion, cmdTypeApplyDelta}
	data = appendUint64TLVField(data, tagApplyDeltaSourceSlotID, uint64(sourceSlotID))
	data = appendUint64TLVField(data, tagApplyDeltaSourceIndex, sourceIndex)
	data = appendUint64TLVField(data, tagApplyDeltaHashSlot, uint64(hashSlot))
	data = appendBytesTLVField(data, tagApplyDeltaOriginalCmd, originalCmd)
	return data
}

func (c *applyDeltaCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	if c.HashSlot != 0 && c.HashSlot != hashSlot {
		return metadb.ErrInvalidArgument
	}
	decoded, err := decodeCommand(c.OriginalCmd)
	if err != nil {
		return err
	}
	if _, ok := decoded.(*applyDeltaCmd); ok {
		return fmt.Errorf("%w: nested apply delta command", metadb.ErrInvalidArgument)
	}
	return decoded.apply(wb, hashSlot)
}

func decodeApplyDelta(data []byte) (command, error) {
	cmd := &applyDeltaCmd{}
	var (
		haveSourceSlot bool
		haveSourceIdx  bool
		haveHashSlot   bool
		haveOriginal   bool
	)

	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n

		switch tag {
		case tagApplyDeltaSourceSlotID:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad apply delta SourceSlotID length", metadb.ErrCorruptValue)
			}
			cmd.SourceSlotID = multiraft.SlotID(binary.BigEndian.Uint64(value))
			haveSourceSlot = true
		case tagApplyDeltaSourceIndex:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad apply delta SourceIndex length", metadb.ErrCorruptValue)
			}
			cmd.SourceIndex = binary.BigEndian.Uint64(value)
			haveSourceIdx = true
		case tagApplyDeltaHashSlot:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad apply delta HashSlot length", metadb.ErrCorruptValue)
			}
			raw := binary.BigEndian.Uint64(value)
			if raw > uint64(^uint16(0)) {
				return nil, fmt.Errorf("%w: bad apply delta HashSlot value %d", metadb.ErrCorruptValue, raw)
			}
			cmd.HashSlot = uint16(raw)
			haveHashSlot = true
		case tagApplyDeltaOriginalCmd:
			cmd.OriginalCmd = append([]byte(nil), value...)
			haveOriginal = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}

	if !haveSourceSlot || !haveSourceIdx || !haveHashSlot || !haveOriginal {
		return nil, fmt.Errorf("%w: incomplete apply delta command", metadb.ErrCorruptValue)
	}
	return cmd, nil
}

type enterFenceCmd struct {
	HashSlot uint16
}

func EncodeEnterFenceCommand(hashSlot uint16) []byte {
	data := []byte{commandVersion, cmdTypeEnterFence}
	data = appendUint64TLVField(data, tagEnterFenceHashSlot, uint64(hashSlot))
	return data
}

func (c *enterFenceCmd) apply(_ *metadb.WriteBatch, hashSlot uint16) error {
	if c.HashSlot != 0 && c.HashSlot != hashSlot {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func decodeEnterFence(data []byte) (command, error) {
	cmd := &enterFenceCmd{}
	var haveHashSlot bool

	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n

		switch tag {
		case tagEnterFenceHashSlot:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad enter fence HashSlot length", metadb.ErrCorruptValue)
			}
			raw := binary.BigEndian.Uint64(value)
			if raw > uint64(^uint16(0)) {
				return nil, fmt.Errorf("%w: bad enter fence HashSlot value %d", metadb.ErrCorruptValue, raw)
			}
			cmd.HashSlot = uint16(raw)
			haveHashSlot = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}

	if !haveHashSlot {
		return nil, fmt.Errorf("%w: incomplete enter fence command", metadb.ErrCorruptValue)
	}
	return cmd, nil
}
