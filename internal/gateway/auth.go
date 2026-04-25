package gateway

import (
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/internal/gateway/types"
	"github.com/WuKongIM/WuKongIM/internal/gateway/wkprotoenc"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type Authenticator = gatewaytypes.Authenticator
type AuthenticatorFunc = gatewaytypes.AuthenticatorFunc
type AuthResult = gatewaytypes.AuthResult

const (
	SessionValueUID             = gatewaytypes.SessionValueUID
	SessionValueDeviceID        = gatewaytypes.SessionValueDeviceID
	SessionValueDeviceFlag      = gatewaytypes.SessionValueDeviceFlag
	SessionValueDeviceLevel     = gatewaytypes.SessionValueDeviceLevel
	SessionValueProtocolVersion = gatewaytypes.SessionValueProtocolVersion
	SessionValueEncryptionEnabled = gatewaytypes.SessionValueEncryptionEnabled
	SessionValueAESKey            = gatewaytypes.SessionValueAESKey
	SessionValueAESIV             = gatewaytypes.SessionValueAESIV
)

type WKProtoAuthOptions struct {
	TokenAuthOn       bool
	EncryptionEnabled bool
	DisableEncryption bool
	NodeID            uint64
	Now               func() time.Time

	IsVisitor   func(uid string) bool
	VerifyToken func(uid string, deviceFlag frame.DeviceFlag, token string) (frame.DeviceLevel, error)
	IsBanned    func(uid string) (bool, error)
}

func NewWKProtoAuthenticator(opts WKProtoAuthOptions) Authenticator {
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	encryptionEnabled := true
	if opts.DisableEncryption {
		encryptionEnabled = false
	}
	if opts.EncryptionEnabled {
		encryptionEnabled = true
	}

	return AuthenticatorFunc(func(_ *Context, connect *frame.ConnectPacket) (*AuthResult, error) {
		if connect == nil {
			return &AuthResult{
				Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonAuthFail},
			}, nil
		}

		deviceLevel := frame.DeviceLevelSlave
		if opts.TokenAuthOn && !isVisitor(opts.IsVisitor, connect.UID) {
			if connect.Token == "" || opts.VerifyToken == nil {
				return &AuthResult{
					Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonAuthFail},
				}, nil
			}

			level, err := opts.VerifyToken(connect.UID, connect.DeviceFlag, connect.Token)
			if err != nil {
				return &AuthResult{
					Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonAuthFail},
				}, nil
			}
			deviceLevel = level
		}

		if opts.IsBanned != nil {
			banned, err := opts.IsBanned(connect.UID)
			if err != nil || banned {
				reason := frame.ReasonBan
				if err != nil {
					reason = frame.ReasonAuthFail
				}
				return &AuthResult{
					Connack: &frame.ConnackPacket{ReasonCode: reason},
				}, nil
			}
		}

		serverVersion := connect.Version
		if serverVersion == 0 || serverVersion > frame.LatestVersion {
			serverVersion = frame.LatestVersion
		}

		connack := &frame.ConnackPacket{
			ReasonCode:    frame.ReasonSuccess,
			TimeDiff:      nowFn().UnixMilli() - connect.ClientTimestamp,
			ServerVersion: serverVersion,
			NodeId:        opts.NodeID,
		}
		connack.HasServerVersion = connect.Version > 3

		sessionValues := map[string]any{
			SessionValueUID:             connect.UID,
			SessionValueDeviceID:        connect.DeviceID,
			SessionValueDeviceFlag:      connect.DeviceFlag,
			SessionValueDeviceLevel:     deviceLevel,
			SessionValueProtocolVersion: serverVersion,
		}
		if encryptionEnabled {
			if connect.ClientKey == "" {
				return &AuthResult{
					Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonClientKeyIsEmpty},
				}, nil
			}
			sessionKeys, serverKey, err := wkprotoenc.NegotiateServerSession(connect.ClientKey)
			if err != nil {
				return &AuthResult{
					Connack: &frame.ConnackPacket{ReasonCode: frame.ReasonAuthFail},
				}, nil
			}
			connack.ServerKey = serverKey
			connack.Salt = string(sessionKeys.AESIV)
			sessionValues[SessionValueEncryptionEnabled] = true
			sessionValues[SessionValueAESKey] = sessionKeys.AESKey
			sessionValues[SessionValueAESIV] = sessionKeys.AESIV
		}

		return &AuthResult{
			Connack: connack,
			SessionValues: sessionValues,
		}, nil
	})
}

func isVisitor(fn func(uid string) bool, uid string) bool {
	if fn == nil {
		return false
	}
	return fn(uid)
}
