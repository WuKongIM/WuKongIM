package server

import (
	"github.com/WuKongIM/WuKongIM/pkg/wraft/transporter"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type CMDType uint32

const (
	CMDUpdateUserToken CMDType = 201
)

func (c CMDType) Uint32() uint32 {
	return uint32(c)
}

type CMDReq transporter.CMDReq

// EncodeUserToken EncodeUserToken
func EncodeUserToken(uid string, deviceFlag uint8, deviceLevel uint8, token string) []byte {
	enc := wkproto.NewEncoder()
	enc.WriteString(uid)
	enc.WriteUint8(deviceFlag)
	enc.WriteUint8(deviceLevel)
	enc.WriteString(token)
	return enc.Bytes()
}

func (c *CMDReq) DecodeUserToken() (uid string, deviceFlag uint8, deviceLevel uint8, token string, err error) {
	decoder := wkproto.NewDecoder(c.Param)
	if uid, err = decoder.String(); err != nil {
		return
	}
	if deviceFlag, err = decoder.Uint8(); err != nil {
		return
	}

	if deviceLevel, err = decoder.Uint8(); err != nil {
		return
	}
	if token, err = decoder.String(); err != nil {
		return
	}
	return
}
