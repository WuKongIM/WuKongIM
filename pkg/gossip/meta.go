package gossip

import (
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

// nodeMeta 数据的大小不能超过 memberlist.MetaMaxSize
type nodeMeta struct {
	version      uint32     // meta数据的版本号
	dataVersion  uint64     // 当前节点数据的版本号
	role         ServerRole // 当前节点的角色
	currentEpoch uint32     // 当前选举周期
}

func (n *nodeMeta) encode() []byte {
	encode := wkproto.NewEncoder()
	defer encode.End()
	encode.WriteUint32(n.version)
	encode.WriteUint64(n.dataVersion)
	encode.WriteUint8(uint8(n.role))
	encode.WriteUint32(n.currentEpoch)
	return encode.Bytes()
}

func decodeNodeMeta(data []byte) (*nodeMeta, error) {
	n := &nodeMeta{}
	decode := wkproto.NewDecoder(data)
	var err error
	if n.version, err = decode.Uint32(); err != nil {
		return nil, err
	}
	if n.dataVersion, err = decode.Uint64(); err != nil {
		return nil, err
	}
	role := uint8(0)
	if role, err = decode.Uint8(); err != nil {
		return nil, err
	}
	n.role = ServerRole(role)

	if n.currentEpoch, err = decode.Uint32(); err != nil {
		return nil, err
	}
	return n, nil
}
