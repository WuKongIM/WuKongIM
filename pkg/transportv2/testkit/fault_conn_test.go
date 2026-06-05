package testkit

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestBlockingConnWriteReturnsErrorAfterReleaseThenClose(t *testing.T) {
	conn := NewBlockingConn()
	conn.ReleaseWrite()
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	n, err := conn.Write([]byte("payload"))
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Write() error = %v, want %v", err, net.ErrClosed)
	}
	if n != 0 {
		t.Fatalf("Write() bytes = %d, want 0", n)
	}
}

func TestBlockingConnCloseUnblocksPendingWriteWithError(t *testing.T) {
	conn := NewBlockingConn()

	result := make(chan error, 1)
	go func() {
		_, err := conn.Write([]byte("payload"))
		result <- err
	}()

	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-result:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Write() error = %v, want %v", err, net.ErrClosed)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Write to unblock")
	}
}
