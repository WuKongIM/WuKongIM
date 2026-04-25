package stdnet

import (
	"net"
	"sync"
	"time"
)

type conn struct {
	id uint64
	net.Conn

	closeOnce sync.Once
}

func newConn(id uint64, raw net.Conn) *conn {
	return &conn{
		id:   id,
		Conn: raw,
	}
}

func (c *conn) ID() uint64 {
	if c == nil {
		return 0
	}
	return c.id
}

func (c *conn) Write(data []byte) error {
	if c == nil || c.Conn == nil {
		return nil
	}

	_, err := c.Conn.Write(data)
	return err
}

func (c *conn) LocalAddr() string {
	if c == nil || c.Conn == nil || c.Conn.LocalAddr() == nil {
		return ""
	}
	return c.Conn.LocalAddr().String()
}

func (c *conn) RemoteAddr() string {
	if c == nil || c.Conn == nil || c.Conn.RemoteAddr() == nil {
		return ""
	}
	return c.Conn.RemoteAddr().String()
}

func (c *conn) Close() error {
	if c == nil || c.Conn == nil {
		return nil
	}

	var err error
	c.closeOnce.Do(func() {
		err = c.Conn.Close()
	})
	return err
}

func (c *conn) SetWriteDeadline(deadline time.Time) error {
	if c == nil || c.Conn == nil {
		return nil
	}
	return c.Conn.SetWriteDeadline(deadline)
}
