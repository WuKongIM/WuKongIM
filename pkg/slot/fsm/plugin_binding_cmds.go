package fsm

import (
	"encoding/binary"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type bindPluginUserCmd struct {
	binding metadb.PluginUserBinding
}

func (c *bindPluginUserCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.BindPluginUser(hashSlot, c.binding)
}

type unbindPluginUserCmd struct {
	uid      string
	pluginNo string
}

func (c *unbindPluginUserCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	return wb.UnbindPluginUser(hashSlot, c.uid, c.pluginNo)
}

// EncodeBindPluginUserCommand encodes a UID-owned plugin binding mutation.
func EncodeBindPluginUserCommand(binding metadb.PluginUserBinding) []byte {
	buf := make([]byte, 0, headerSize+len(binding.UID)+len(binding.PluginNo)+32)
	buf = append(buf, commandVersion, cmdTypeBindPluginUser)
	buf = appendStringTLVField(buf, tagPluginUserBindingUID, binding.UID)
	buf = appendStringTLVField(buf, tagPluginUserBindingPluginNo, binding.PluginNo)
	buf = appendInt64TLVField(buf, tagPluginUserBindingCreatedAtMS, binding.CreatedAtMS)
	buf = appendInt64TLVField(buf, tagPluginUserBindingUpdatedAtMS, binding.UpdatedAtMS)
	return buf
}

// EncodeUnbindPluginUserCommand encodes a UID-owned plugin unbind mutation.
func EncodeUnbindPluginUserCommand(uid, pluginNo string) []byte {
	buf := make([]byte, 0, headerSize+len(uid)+len(pluginNo)+16)
	buf = append(buf, commandVersion, cmdTypeUnbindPluginUser)
	buf = appendStringTLVField(buf, tagPluginUserBindingUID, uid)
	buf = appendStringTLVField(buf, tagPluginUserBindingPluginNo, pluginNo)
	return buf
}

func decodeBindPluginUser(data []byte) (command, error) {
	binding, haveUID, havePluginNo, haveCreated, haveUpdated, err := decodePluginUserBindingFields(data)
	if err != nil {
		return nil, err
	}
	if !haveUID || !havePluginNo || !haveCreated || !haveUpdated {
		return nil, fmt.Errorf("%w: incomplete bind plugin user command", metadb.ErrCorruptValue)
	}
	return &bindPluginUserCmd{binding: binding}, nil
}

func decodeUnbindPluginUser(data []byte) (command, error) {
	binding, haveUID, havePluginNo, _, _, err := decodePluginUserBindingFields(data)
	if err != nil {
		return nil, err
	}
	if !haveUID || !havePluginNo {
		return nil, fmt.Errorf("%w: incomplete unbind plugin user command", metadb.ErrCorruptValue)
	}
	return &unbindPluginUserCmd{uid: binding.UID, pluginNo: binding.PluginNo}, nil
}

func decodePluginUserBindingFields(data []byte) (binding metadb.PluginUserBinding, haveUID bool, havePluginNo bool, haveCreated bool, haveUpdated bool, err error) {
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return metadb.PluginUserBinding{}, false, false, false, false, err
		}
		off += n
		switch tag {
		case tagPluginUserBindingUID:
			binding.UID = string(value)
			haveUID = true
		case tagPluginUserBindingPluginNo:
			binding.PluginNo = string(value)
			havePluginNo = true
		case tagPluginUserBindingCreatedAtMS:
			if len(value) != 8 {
				return metadb.PluginUserBinding{}, false, false, false, false, fmt.Errorf("%w: bad plugin binding CreatedAtMS length", metadb.ErrCorruptValue)
			}
			binding.CreatedAtMS = int64(binary.BigEndian.Uint64(value))
			haveCreated = true
		case tagPluginUserBindingUpdatedAtMS:
			if len(value) != 8 {
				return metadb.PluginUserBinding{}, false, false, false, false, fmt.Errorf("%w: bad plugin binding UpdatedAtMS length", metadb.ErrCorruptValue)
			}
			binding.UpdatedAtMS = int64(binary.BigEndian.Uint64(value))
			haveUpdated = true
		default:
			// Unknown tag — skip for forward compatibility.
		}
	}
	return binding, haveUID, havePluginNo, haveCreated, haveUpdated, nil
}
