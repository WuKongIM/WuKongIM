package wkserver

import "go.uber.org/atomic"

type connContext struct {
	uid atomic.String
}

func newConnContext(uid string) *connContext {
	c := &connContext{}
	c.uid.Store(uid)
	return c
}
