package clusterconfig

import (
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type CMDType uint16

const (
	CMDTypeUnknown                   CMDType = iota
	CMDTypeConfigChange                      // 配置改变
	CMDTypeConfigApiServerAddrChange         // api服务地址变更
	CMDTypeNodeJoin                          // 节点加入
	CMDTypeNodeJoining                       // 节点加入中
	CMDTypeNodeJoined                        // 节点已加入
	CMDTypeNodeOnlineStatusChange            // 节点在线状态改变
	CMDTypeSlotMigrate                       // 槽迁移
	CMDTypeSlotUpdate                        // 槽更新
	CMDTypeNodeStatusChange                  // 节点状态改变

)

func (c CMDType) Uint16() uint16 {
	return uint16(c)
}

func (c CMDType) String() string {
	switch c {
	case CMDTypeConfigChange:
		return "CMDTypeConfigChange"
	case CMDTypeConfigApiServerAddrChange:
		return "CMDTypeConfigApiServerAddrChange"
	case CMDTypeNodeJoin:
		return "CMDTypeNodeJoin"
	case CMDTypeNodeJoining:
		return "CMDTypeNodeJoining"
	case CMDTypeNodeJoined:
		return "CMDTypeNodeJoined"
	case CMDTypeNodeOnlineStatusChange:
		return "CMDTypeNodeOnlineStatusChange"
	case CMDTypeSlotMigrate:
		return "CMDTypeSlotMigrate"
	case CMDTypeSlotUpdate:
		return "CMDTypeSlotUpdate"
	case CMDTypeNodeStatusChange:
		return "CMDTypeNodeStatusChange"
	}
	return "CMDTypeUnknown"
}

type CMD struct {
	CmdType CMDType
	Data    []byte
	version uint16 // 数据协议版本

}

func NewCMD(cmdType CMDType, data []byte) *CMD {
	return &CMD{
		CmdType: cmdType,
		Data:    data,
	}
}

func (c *CMD) Marshal() ([]byte, error) {
	c.version = 1
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(c.version)
	enc.WriteUint16(c.CmdType.Uint16())
	enc.WriteBytes(c.Data)
	return enc.Bytes(), nil

}

func (c *CMD) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.version, err = dec.Uint16(); err != nil {
		return err
	}
	var cmdType uint16
	if cmdType, err = dec.Uint16(); err != nil {
		return err
	}
	c.CmdType = CMDType(cmdType)
	if c.Data, err = dec.BinaryAll(); err != nil {
		return err
	}
	return nil
}

func (c *CMD) CMDContent() (string, error) {
	switch c.CmdType {
	case CMDTypeConfigChange:
		cfg := &types.Config{}
		if err := cfg.Unmarshal(c.Data); err != nil {
			return "", err
		}
		return wkutil.ToJSON(cfg), nil
	case CMDTypeConfigApiServerAddrChange:
		nodeId, apiServerAddr, err := DecodeApiServerAddrChange(c.Data)
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"nodeId":        nodeId,
			"apiServerAddr": apiServerAddr,
		}), nil

	case CMDTypeNodeJoin:
		node := &types.Node{}
		err := node.Unmarshal(c.Data)
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(node), nil
	case CMDTypeNodeJoining:
		nodeId := binary.BigEndian.Uint64(c.Data)
		return wkutil.ToJSON(map[string]interface{}{
			"nodeId": nodeId,
		}), nil
	case CMDTypeNodeJoined:
		nodeId, slots, err := DecodeNodeJoined(c.Data)
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"nodeId": nodeId,
			"slots":  slots,
		}), nil

	case CMDTypeNodeOnlineStatusChange:
		nodeId, online, err := DecodeNodeOnlineStatusChange(c.Data)
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"nodeId": nodeId,
			"online": online,
		}), nil
	case CMDTypeSlotMigrate:
		slotId, fromNodeId, toNodeId, err := DecodeMigrateSlot(c.Data)
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"slotId":     slotId,
			"fromNodeId": fromNodeId,
			"toNodeId":   toNodeId,
		}), nil
	case CMDTypeSlotUpdate:
		slotset := types.SlotSet{}
		err := slotset.Unmarshal(c.Data)
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(slotset), nil
	case CMDTypeNodeStatusChange:
		nodeId, status, err := DecodeNodeStatusChange(c.Data)
		if err != nil {
			return "", err
		}
		return wkutil.ToJSON(map[string]interface{}{
			"nodeId": nodeId,
			"status": status,
		}), nil
	}

	return "", nil
}

func EncodeApiServerAddrChange(nodeId uint64, apiServerAddr string) ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(nodeId)
	enc.WriteString(apiServerAddr)
	return enc.Bytes(), nil
}

func DecodeApiServerAddrChange(data []byte) (uint64, string, error) {
	dec := wkproto.NewDecoder(data)
	var err error
	var nodeId uint64
	if nodeId, err = dec.Uint64(); err != nil {
		return 0, "", err
	}
	apiServerAddr, err := dec.String()
	return nodeId, apiServerAddr, err
}

func EncodeNodeOnlineStatusChange(nodeId uint64, online bool) ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(nodeId)
	enc.WriteUint8(wkutil.BoolToUint8(online))
	return enc.Bytes(), nil
}

func DecodeNodeOnlineStatusChange(data []byte) (uint64, bool, error) {
	dec := wkproto.NewDecoder(data)
	var err error
	var nodeId uint64
	if nodeId, err = dec.Uint64(); err != nil {
		return 0, false, err
	}
	online, err := dec.Uint8()
	return nodeId, wkutil.Uint8ToBool(online), err
}

func EncodeMigrateSlot(slotId uint32, fromNodeId, toNodeId uint64) ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteUint32(slotId)
	enc.WriteUint64(fromNodeId)
	enc.WriteUint64(toNodeId)

	return enc.Bytes(), nil
}

func DecodeMigrateSlot(data []byte) (slotId uint32, fromNodeId, toNodeId uint64, err error) {
	dec := wkproto.NewDecoder(data)
	if slotId, err = dec.Uint32(); err != nil {
		return
	}

	if fromNodeId, err = dec.Uint64(); err != nil {
		return
	}

	if toNodeId, err = dec.Uint64(); err != nil {
		return
	}

	return
}

func EncodeNodeStatusChange(nodeId uint64, status types.NodeStatus) ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(nodeId)
	enc.WriteUint32(uint32(status))
	return enc.Bytes(), nil
}

func DecodeNodeStatusChange(data []byte) (uint64, types.NodeStatus, error) {
	dec := wkproto.NewDecoder(data)
	var err error
	var nodeId uint64
	if nodeId, err = dec.Uint64(); err != nil {
		return 0, types.NodeStatus_NodeStatusUnkown, err
	}
	status, err := dec.Uint32()
	return nodeId, types.NodeStatus(status), err
}

func EncodeNodeJoined(nodeId uint64, slots []*types.Slot) ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(nodeId)
	for _, slot := range slots {
		data, err := slot.Marshal()
		if err != nil {
			return nil, err
		}
		enc.WriteBinary(data)
	}
	return enc.Bytes(), nil
}

func DecodeNodeJoined(data []byte) (uint64, []*types.Slot, error) {
	dec := wkproto.NewDecoder(data)
	var err error
	var nodeId uint64
	if nodeId, err = dec.Uint64(); err != nil {
		return 0, nil, err
	}
	var slots []*types.Slot
	for dec.Len() > 0 {
		data, err := dec.Binary()
		if err != nil {
			return 0, nil, err
		}
		slot := &types.Slot{}
		if err := slot.Unmarshal(data); err != nil {
			return 0, nil, err
		}
		slots = append(slots, slot)
	}
	return nodeId, slots, nil
}

func EncodeLeaderChange(leaderId uint64) ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(leaderId)
	return enc.Bytes(), nil
}

func DecodeLeaderChange(data []byte) (uint64, error) {
	dec := wkproto.NewDecoder(data)
	var err error
	var leaderId uint64
	if leaderId, err = dec.Uint64(); err != nil {
		return 0, err
	}
	return leaderId, nil
}
