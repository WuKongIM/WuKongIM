package gateway

import (
	gatewaysession "github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type onlineSessionAdapter struct {
	session gatewaysession.Session
}

var _ online.SessionWriter = onlineSessionAdapter{}

func newOnlineSessionAdapter(session gatewaysession.Session) online.SessionWriter {
	if session == nil {
		return nil
	}
	return onlineSessionAdapter{session: session}
}

func (a onlineSessionAdapter) WriteFrame(f frame.Frame) error {
	if a.session == nil {
		return gatewaysession.ErrSessionClosed
	}
	return a.session.WriteFrame(f)
}

func (a onlineSessionAdapter) Close() error {
	if a.session == nil {
		return nil
	}
	return a.session.Close()
}

func (a onlineSessionAdapter) ID() uint64 {
	if a.session == nil {
		return 0
	}
	return a.session.ID()
}

func (a onlineSessionAdapter) Listener() string {
	if a.session == nil {
		return ""
	}
	return a.session.Listener()
}

func (a onlineSessionAdapter) RemoteAddr() string {
	if a.session == nil {
		return ""
	}
	return a.session.RemoteAddr()
}

func (a onlineSessionAdapter) LocalAddr() string {
	if a.session == nil {
		return ""
	}
	return a.session.LocalAddr()
}

func (a onlineSessionAdapter) SetValue(key string, value any) {
	if a.session == nil {
		return
	}
	a.session.SetValue(key, value)
}

func (a onlineSessionAdapter) Value(key string) any {
	if a.session == nil {
		return nil
	}
	return a.session.Value(key)
}
