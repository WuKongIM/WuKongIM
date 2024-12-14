package reactor

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

// type Conn interface {
// 	// ConnId  用户唯一连接id
// 	ConnId() int64
// 	// Uid 用户uid
// 	Uid() string
// 	// DeviceId 设备id
// 	DeviceId() string
// 	// DeviceFlag 设备标记
// 	DeviceFlag() wkproto.DeviceFlag
// 	// FromNode 连接属于节点
// 	FromNode() uint64
// 	// SetAuth 设置是否认证
// 	SetAuth(auth bool)
// 	// IsAuth 是否认证
// 	IsAuth() bool
// 	// Equal 判断是否相等
// 	Equal(conn Conn) bool
// 	// SetString 设置扩展字段
// 	SetString(key string, value string)

// 	SetProtoVersion(version uint8)
// 	GetProtoVersion() uint8

// 	DeviceLevel() wkproto.DeviceLevel
// 	SetDeviceLevel(level wkproto.DeviceLevel)

// 	// Encode 编码连接数据
// 	Encode() ([]byte, error)
// 	// Decode 解密连接数据
// 	Decode(data []byte) error
// }

// const (
// 	ConnAesIV  string = "aesIV"
// 	ConnAesKey string = "aesKey"
// )

type Conn struct {
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

func (c *Conn) Equal(cn *Conn) bool {

	return c.Uid == cn.Uid && c.ConnId == cn.ConnId && c.FromNode == cn.FromNode
}
