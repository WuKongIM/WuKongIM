package reactor

import (
	"fmt"
	"sync/atomic"
	"time"

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
	ConnId       int64
	Uid          string
	DeviceId     string
	DeviceFlag   wkproto.DeviceFlag
	DeviceLevel  wkproto.DeviceLevel
	FromNode     uint64
	Auth         bool
	AesIV        []byte
	AesKey       []byte
	ProtoVersion uint8

	// 不编码
	Uptime time.Time // 连接创建时间
}

func (c *Conn) Encode() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteInt64(c.ConnId)
	enc.WriteString(c.Uid)
	enc.WriteString(c.DeviceId)
	enc.WriteUint8(c.DeviceFlag.ToUint8())
	enc.WriteUint8(uint8(c.DeviceLevel))
	enc.WriteUint64(c.FromNode)
	enc.WriteUint8(wkutil.BoolToUint8(c.Auth))
	enc.WriteBinary(c.AesIV)
	enc.WriteBinary(c.AesKey)
	enc.WriteUint8(c.ProtoVersion)
	return enc.Bytes(), nil
}

func (c *Conn) Decode(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.ConnId, err = dec.Int64(); err != nil {
		return err
	}
	if c.Uid, err = dec.String(); err != nil {
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
	if c.FromNode, err = dec.Uint64(); err != nil {
		return err
	}
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

	return nil
}

func (c *Conn) Size() uint64 {
	return uint64(8 + len(c.Uid) + len(c.DeviceId) + 1 + 1 + 8 + 1 + len(c.AesIV) + len(c.AesKey) + 1)
}

func (c *Conn) Equal(cn *Conn) bool {

	return c.Uid == cn.Uid && c.ConnId == cn.ConnId && c.FromNode == cn.FromNode
}

func (c *Conn) String() string {

	return fmt.Sprintf("ConnId:%d, Uid:%s, DeviceId:%s, DeviceFlag:%d, DeviceLevel:%d, FromNode:%d, Auth:%v, ProtoVersion:%d, AesIV:%s, AesKey:%s", c.ConnId, c.Uid, c.DeviceId, c.DeviceFlag, c.DeviceLevel, c.FromNode, c.Auth, c.ProtoVersion, c.AesIV, c.AesKey)
}
