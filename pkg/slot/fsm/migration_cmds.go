package fsm

import (
	"encoding/binary"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	cmdTypeApplyDelta             uint8 = 20
	cmdTypeEnterFence             uint8 = 21
	cmdTypeAckMigrationOutbox     uint8 = 22
	cmdTypeCleanupMigrationOutbox uint8 = 23

	tagApplyDeltaSourceSlotID uint8 = 1
	tagApplyDeltaSourceIndex  uint8 = 2
	tagApplyDeltaHashSlot     uint8 = 3
	tagApplyDeltaOriginalCmd  uint8 = 4

	tagEnterFenceHashSlot uint8 = 1
	tagEnterFenceTarget   uint8 = 2

	tagMigrationOutboxHashSlot    uint8 = 1
	tagMigrationOutboxSourceSlot  uint8 = 2
	tagMigrationOutboxTargetSlot  uint8 = 3
	tagMigrationOutboxSourceIndex uint8 = 4
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
	if c.HashSlot != hashSlot {
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
	Target   multiraft.SlotID
}

func EncodeEnterFenceCommand(hashSlot uint16) []byte {
	data := []byte{commandVersion, cmdTypeEnterFence}
	data = appendUint64TLVField(data, tagEnterFenceHashSlot, uint64(hashSlot))
	return data
}

func EncodeEnterFenceCommandForTarget(hashSlot uint16, target multiraft.SlotID) []byte {
	data := EncodeEnterFenceCommand(hashSlot)
	data = appendUint64TLVField(data, tagEnterFenceTarget, uint64(target))
	return data
}

func (c *enterFenceCmd) apply(_ *metadb.WriteBatch, hashSlot uint16) error {
	if c.HashSlot != hashSlot {
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
		case tagEnterFenceTarget:
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: bad enter fence Target length", metadb.ErrCorruptValue)
			}
			cmd.Target = multiraft.SlotID(binary.BigEndian.Uint64(value))
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}

	if !haveHashSlot {
		return nil, fmt.Errorf("%w: incomplete enter fence command", metadb.ErrCorruptValue)
	}
	return cmd, nil
}

type ackMigrationOutboxCmd struct {
	HashSlot    uint16
	SourceSlot  multiraft.SlotID
	TargetSlot  multiraft.SlotID
	SourceIndex uint64
}

func EncodeAckHashSlotMigrationOutboxCommand(hashSlot uint16, sourceSlot, targetSlot multiraft.SlotID, sourceIndex uint64) []byte {
	data := []byte{commandVersion, cmdTypeAckMigrationOutbox}
	data = appendUint64TLVField(data, tagMigrationOutboxHashSlot, uint64(hashSlot))
	data = appendUint64TLVField(data, tagMigrationOutboxSourceSlot, uint64(sourceSlot))
	data = appendUint64TLVField(data, tagMigrationOutboxTargetSlot, uint64(targetSlot))
	data = appendUint64TLVField(data, tagMigrationOutboxSourceIndex, sourceIndex)
	return data
}

func (c *ackMigrationOutboxCmd) apply(_ *metadb.WriteBatch, hashSlot uint16) error {
	if c.HashSlot != 0 && c.HashSlot != hashSlot {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func decodeAckMigrationOutbox(data []byte) (command, error) {
	cmd := &ackMigrationOutboxCmd{}
	seen, err := decodeMigrationOutboxIdentity(data, &cmd.HashSlot, &cmd.SourceSlot, &cmd.TargetSlot, &cmd.SourceIndex)
	if err != nil {
		return nil, err
	}
	if !seen.hashSlot || !seen.sourceSlot || !seen.targetSlot || !seen.sourceIndex {
		return nil, fmt.Errorf("%w: incomplete migration outbox ack command", metadb.ErrCorruptValue)
	}
	return cmd, nil
}

type cleanupMigrationOutboxCmd struct {
	HashSlot     uint16
	SourceSlot   multiraft.SlotID
	TargetSlot   multiraft.SlotID
	ThroughIndex uint64
}

func EncodeCleanupHashSlotMigrationOutboxCommand(hashSlot uint16, sourceSlot, targetSlot multiraft.SlotID, throughIndex uint64) []byte {
	data := []byte{commandVersion, cmdTypeCleanupMigrationOutbox}
	data = appendUint64TLVField(data, tagMigrationOutboxHashSlot, uint64(hashSlot))
	data = appendUint64TLVField(data, tagMigrationOutboxSourceSlot, uint64(sourceSlot))
	data = appendUint64TLVField(data, tagMigrationOutboxTargetSlot, uint64(targetSlot))
	data = appendUint64TLVField(data, tagMigrationOutboxSourceIndex, throughIndex)
	return data
}

func (c *cleanupMigrationOutboxCmd) apply(_ *metadb.WriteBatch, hashSlot uint16) error {
	if c.HashSlot != 0 && c.HashSlot != hashSlot {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func decodeCleanupMigrationOutbox(data []byte) (command, error) {
	cmd := &cleanupMigrationOutboxCmd{}
	seen, err := decodeMigrationOutboxIdentity(data, &cmd.HashSlot, &cmd.SourceSlot, &cmd.TargetSlot, &cmd.ThroughIndex)
	if err != nil {
		return nil, err
	}
	if !seen.hashSlot || !seen.sourceSlot || !seen.targetSlot || !seen.sourceIndex {
		return nil, fmt.Errorf("%w: incomplete migration outbox cleanup command", metadb.ErrCorruptValue)
	}
	return cmd, nil
}

type migrationOutboxIdentitySeen struct {
	hashSlot    bool
	sourceSlot  bool
	targetSlot  bool
	sourceIndex bool
}

func decodeMigrationOutboxIdentity(data []byte, hashSlot *uint16, sourceSlot, targetSlot *multiraft.SlotID, sourceIndex *uint64) (migrationOutboxIdentitySeen, error) {
	var seen migrationOutboxIdentitySeen
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return seen, err
		}
		off += n

		if len(value) != 8 {
			return seen, fmt.Errorf("%w: bad migration outbox field length", metadb.ErrCorruptValue)
		}
		raw := binary.BigEndian.Uint64(value)
		switch tag {
		case tagMigrationOutboxHashSlot:
			if raw > uint64(^uint16(0)) {
				return seen, fmt.Errorf("%w: bad migration outbox HashSlot value %d", metadb.ErrCorruptValue, raw)
			}
			if hashSlot != nil {
				*hashSlot = uint16(raw)
			}
			seen.hashSlot = true
		case tagMigrationOutboxSourceSlot:
			if sourceSlot != nil {
				*sourceSlot = multiraft.SlotID(raw)
			}
			seen.sourceSlot = true
		case tagMigrationOutboxTargetSlot:
			if targetSlot != nil {
				*targetSlot = multiraft.SlotID(raw)
			}
			seen.targetSlot = true
		case tagMigrationOutboxSourceIndex:
			if sourceIndex != nil {
				*sourceIndex = raw
			}
			seen.sourceIndex = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	return seen, nil
}
