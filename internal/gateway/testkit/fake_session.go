package testkit

import "github.com/WuKongIM/WuKongIM/internal/gateway/session"

func NewProtocolSession() session.Session {
	return session.New(session.Config{
		ID:               1,
		Listener:         "fake-listener",
		RemoteAddr:       "fake-remote",
		LocalAddr:        "fake-local",
		WriteQueueSize:   1,
		MaxOutboundBytes: 1024,
	})
}
