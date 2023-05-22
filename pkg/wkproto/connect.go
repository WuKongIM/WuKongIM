package wkproto

import (
	"fmt"

	"github.com/pkg/errors"
)

// ConnectPacket 连接包
type ConnectPacket struct {
	Framer
	Version         uint8      // 协议版本
	ClientKey       string     // 客户端公钥
	DeviceID        string     // 设备ID
	DeviceFlag      DeviceFlag // 设备标示(同标示同账号互踢)
	ClientTimestamp int64      // 客户端当前时间戳(13位时间戳,到毫秒)
	UID             string     // 用户ID
	Token           string     // token
}

// ToFixHeaderUint8 ToFixHeaderUint8
func (c ConnectPacket) ToFixHeaderUint8() uint8 {
	typeAndFlags := encodeBool(c.GetDUP())<<3 | encodeBool(c.GetsyncOnce())<<2 | encodeBool(c.GetRedDot())<<1 | encodeBool(c.GetNoPersist())
	return byte(int(c.GetFrameType()<<4) | typeAndFlags)
}

// GetFrameType 包类型
func (c ConnectPacket) GetFrameType() FrameType {
	return CONNECT
}

func (c ConnectPacket) String() string {
	return fmt.Sprintf(" UID:%s DeviceFlag:%d DeviceId:%s ClientTimestamp:%d  Token:%s Version:%d", c.UID, c.DeviceFlag, c.DeviceID, c.ClientTimestamp, c.Token, c.Version)
}

func decodeConnect(frame Frame, data []byte, version uint8) (Frame, error) {
	dec := NewDecoder(data)
	connectPacket := &ConnectPacket{}
	connectPacket.Framer = frame.(Framer)
	var err error
	if connectPacket.Version, err = dec.Uint8(); err != nil {
		return nil, errors.Wrap(err, "解码version失败！")
	}
	var deviceFlag uint8
	if deviceFlag, err = dec.Uint8(); err != nil {
		return nil, errors.Wrap(err, "解码DeviceFlag失败！")
	}
	connectPacket.DeviceFlag = DeviceFlag(deviceFlag)
	// DeviceId
	if connectPacket.DeviceID, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码DeviceId失败！")
	}

	if connectPacket.UID, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码UID失败！")
	}
	if connectPacket.Token, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码Token失败！")
	}
	if connectPacket.ClientTimestamp, err = dec.Int64(); err != nil {
		return nil, errors.Wrap(err, "解码ClientTimestamp失败！")
	}
	if connectPacket.ClientKey, err = dec.String(); err != nil {
		return nil, errors.Wrap(err, "解码ClientKey失败！")
	}
	return connectPacket, err
}

func encodeConnect(connectPacket *ConnectPacket, enc *Encoder, version uint8) error {
	// 协议版本
	enc.WriteUint8(connectPacket.Version)
	// 设备标示
	enc.WriteUint8(connectPacket.DeviceFlag.ToUint8())
	// DeviceId
	enc.WriteString(connectPacket.DeviceID)
	// 用户uid
	enc.WriteString(connectPacket.UID)
	// 用户token
	enc.WriteString(connectPacket.Token)
	// 客户端时间戳
	enc.WriteInt64(connectPacket.ClientTimestamp)
	// clientKey
	enc.WriteString(connectPacket.ClientKey)

	return nil
}

func encodeConnectSize(connectPacket *ConnectPacket, version uint8) int {

	var size = 0

	size += VersionByteSize

	size += DeviceFlagByteSize

	size += (len(connectPacket.DeviceID) + StringFixLenByteSize)

	size += (len(connectPacket.UID) + StringFixLenByteSize)

	size += (len(connectPacket.Token) + StringFixLenByteSize)

	size += ClientTimestampByteSize

	size += (len(connectPacket.ClientKey) + StringFixLenByteSize)

	return size
}
