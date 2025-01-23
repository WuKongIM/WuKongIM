package eventbus

import (
	"fmt"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

// 连接统计
type ConnStats struct {
	InPacketCount  atomic.Int64 // 输入包数量
	OutPacketCount atomic.Int64 // 输出包数量

	InPacketByteCount  atomic.Int64 // 输入包字节数量
	OutPacketByteCount atomic.Int64 // 输出包字节数量

	InMsgCount  atomic.Int64 // 输入消息数量
	OutMsgCount atomic.Int64 // 输出消息数量

	InMsgByteCount  atomic.Int64 // 输入消息字节数量
	OutMsgByteCount atomic.Int64 // 输出消息字节数量
}

type Conn struct {
	ConnStats
	// 连接属于用户
	Uid string
	// 连接属于节点
	NodeId uint64
	// 连接id
	ConnId int64
	// 对应设备的id
	DeviceId string
	// 对应设备的标记
	DeviceFlag wkproto.DeviceFlag
	// 对应设备的等级
	DeviceLevel wkproto.DeviceLevel
	// 连接是否通过认证
	Auth bool
	// 连接的aes iv
	AesIV []byte
	// 连接的aes key
	AesKey []byte
	// 连接的通讯协议版本
	ProtoVersion uint8
	// 启动时间
	Uptime uint64

	// 不参与编码
	LastActive uint64 // 最后一次活动时间单位秒
}

func (c *Conn) Encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteString(c.Uid)
	enc.WriteUint64(c.NodeId)
	enc.WriteInt64(c.ConnId)
	enc.WriteString(c.DeviceId)
	enc.WriteUint8(c.DeviceFlag.ToUint8())
	enc.WriteUint8(uint8(c.DeviceLevel))
	enc.WriteUint8(wkutil.BoolToUint8(c.Auth))
	enc.WriteBinary(c.AesIV)
	enc.WriteBinary(c.AesKey)
	enc.WriteUint8(c.ProtoVersion)
	enc.WriteUint64(c.Uptime)
	return enc.Bytes(), nil
}

func (c *Conn) Decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.Uid, err = dec.String(); err != nil {
		return err
	}
	if c.NodeId, err = dec.Uint64(); err != nil {
		return err
	}
	if c.ConnId, err = dec.Int64(); err != nil {
		return err
	}
	if c.DeviceId, err = dec.String(); err != nil {
		return err
	}
	var deviceFlag uint8
	if deviceFlag, err = dec.Uint8(); err != nil {
		return err
	}
	c.DeviceFlag = wkproto.DeviceFlag(deviceFlag)

	var deviceLevel uint8
	if deviceLevel, err = dec.Uint8(); err != nil {
		return err
	}
	c.DeviceLevel = wkproto.DeviceLevel(deviceLevel)

	var auth uint8
	if auth, err = dec.Uint8(); err != nil {
		return err
	}
	c.Auth = wkutil.Uint8ToBool(auth)

	if c.AesIV, err = dec.Binary(); err != nil {
		return err
	}
	if c.AesKey, err = dec.Binary(); err != nil {
		return err
	}

	if c.ProtoVersion, err = dec.Uint8(); err != nil {
		return err
	}
	if c.Uptime, err = dec.Uint64(); err != nil {
		return err
	}

	return nil
}

func (c *Conn) Size() uint64 {
	return uint64(8 + len(c.Uid) + len(c.DeviceId) + 1 + 1 + 8 + 1 + len(c.AesIV) + len(c.AesKey) + 1)
}

func (c *Conn) Equal(cn *Conn) bool {

	return c.Uid == cn.Uid && c.ConnId == cn.ConnId && c.NodeId == cn.NodeId
}

func (c *Conn) String() string {

	return fmt.Sprintf("ConnId:%d, Uid:%s, DeviceId:%s, DeviceFlag:%d, DeviceLevel:%d, NodeId:%d, Auth:%v, ProtoVersion:%d, AesIV:%s, AesKey:%s", c.ConnId, c.Uid, c.DeviceId, c.DeviceFlag, c.DeviceLevel, c.NodeId, c.Auth, c.ProtoVersion, c.AesIV, c.AesKey)
}
