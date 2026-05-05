package core

import (
	"bytes"
	"errors"
	"runtime"
	"strconv"
	"testing"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/internal/gateway/types"
)

var errWriteFromHelperGoroutine = errors.New("write called from timeout helper goroutine")

func TestServerWritePayloadDoesNotSpawnTimeoutGoroutineForTransportManagedWrites(t *testing.T) {
	conn := &sameGoroutineWriteConn{caller: currentGoroutineID(t)}
	srv := &Server{options: gatewaytypes.Options{DefaultSession: gatewaytypes.SessionOptions{WriteTimeout: time.Second}}}
	state := &sessionState{server: srv, conn: conn, closedCh: make(chan struct{})}

	if err := srv.writePayload(state, []byte("payload")); err != nil {
		t.Fatalf("writePayload() error = %v, want nil", err)
	}
}

func TestServerWritePayloadStillTimesOutBlockingConn(t *testing.T) {
	conn := &blockingWriteConn{release: make(chan struct{})}
	t.Cleanup(func() { close(conn.release) })
	srv := &Server{options: gatewaytypes.Options{DefaultSession: gatewaytypes.SessionOptions{WriteTimeout: time.Millisecond}}}
	state := &sessionState{server: srv, conn: conn, closedCh: make(chan struct{})}

	err := srv.writePayload(state, []byte("payload"))
	if !errors.Is(err, gatewaytypes.ErrWriteTimeout) {
		t.Fatalf("writePayload() error = %v, want %v", err, gatewaytypes.ErrWriteTimeout)
	}
}

type sameGoroutineWriteConn struct {
	caller uint64
}

func (c *sameGoroutineWriteConn) ID() uint64                         { return 1 }
func (c *sameGoroutineWriteConn) Close() error                       { return nil }
func (c *sameGoroutineWriteConn) LocalAddr() string                  { return "local" }
func (c *sameGoroutineWriteConn) RemoteAddr() string                 { return "remote" }
func (c *sameGoroutineWriteConn) TransportManagedWriteTimeout() bool { return true }

func (c *sameGoroutineWriteConn) Write([]byte) error {
	if currentGoroutineID(nil) != c.caller {
		return errWriteFromHelperGoroutine
	}
	return nil
}

type blockingWriteConn struct {
	release chan struct{}
}

func (c *blockingWriteConn) ID() uint64         { return 2 }
func (c *blockingWriteConn) Close() error       { return nil }
func (c *blockingWriteConn) LocalAddr() string  { return "local" }
func (c *blockingWriteConn) RemoteAddr() string { return "remote" }

func (c *blockingWriteConn) Write([]byte) error {
	<-c.release
	return nil
}

func currentGoroutineID(t *testing.T) uint64 {
	if t != nil {
		t.Helper()
	}
	buf := make([]byte, 64)
	n := runtime.Stack(buf, false)
	fields := bytes.Fields(buf[:n])
	if len(fields) < 2 {
		if t != nil {
			t.Fatalf("unexpected goroutine stack header: %q", buf[:n])
		}
		return 0
	}
	id, err := strconv.ParseUint(string(fields[1]), 10, 64)
	if err != nil {
		if t != nil {
			t.Fatalf("parse goroutine id from %q: %v", fields[1], err)
		}
		return 0
	}
	return id
}
