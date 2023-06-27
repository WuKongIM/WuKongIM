package wknet

import (
	"errors"
	"net"
	"syscall"

	"golang.org/x/sys/windows"
)

type NetFd struct {
	conn net.Conn
	fd   int
}

func newNetFd(conn net.Conn) NetFd {
	fd, err := dupConn(conn)
	if err != nil {
		panic(err)
	}
	return NetFd{
		conn: conn,
		fd:   fd,
	}
}

func (n NetFd) Read(b []byte) (int, error) {
	return n.conn.Read(b)
}

func (n NetFd) Write(b []byte) (int, error) {
	return n.conn.Write(b)
}

func (n NetFd) Close() error {
	return n.conn.Close()
}

func (n NetFd) Fd() int {
	return n.fd
}

func dupConn(conn net.Conn) (int, error) {
	sc, ok := conn.(interface {
		SyscallConn() (syscall.RawConn, error)
	})
	if !ok {
		return 0, errors.New("RawConn Unsupported")
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return 0, errors.New("RawConn Unsupported")
	}

	var dupHandle windows.Handle
	e := rc.Control(func(fd uintptr) {
		process := windows.CurrentProcess()
		err = windows.DuplicateHandle(
			process,
			windows.Handle(fd),
			process,
			&dupHandle,
			0,
			true,
			windows.DUPLICATE_SAME_ACCESS,
		)
	})
	if err != nil {
		return -1, err
	}

	if e != nil {
		return -1, e
	}

	return int(dupHandle), nil
}
