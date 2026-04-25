package types

import "github.com/WuKongIM/WuKongIM/pkg/protocol/frame"

type Authenticator interface {
	Authenticate(ctx *Context, connect *frame.ConnectPacket) (*AuthResult, error)
}

type AuthenticatorFunc func(ctx *Context, connect *frame.ConnectPacket) (*AuthResult, error)

func (f AuthenticatorFunc) Authenticate(ctx *Context, connect *frame.ConnectPacket) (*AuthResult, error) {
	if f == nil {
		return nil, nil
	}
	return f(ctx, connect)
}

type AuthResult struct {
	Connack       *frame.ConnackPacket
	SessionValues map[string]any
}
