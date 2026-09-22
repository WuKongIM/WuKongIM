//go:build integration

package conn

import (
	"context"
	"sync"
	"testing"
	"time"
)

// A slow cancellation must not hold the request table and delay unrelated
// tracking/finish operations on the same connection generation.
func TestInboundCancelAllowsUnrelatedTracking(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Conn{ctx: parent}
	_, finish, err := c.TrackInbound(1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	entered, release, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	original := c.inboundRequests[1]
	c.inboundRequests[1] = func() { close(entered); <-release; original() }
	go func() { c.CancelInbound(1); close(canceled) }()
	defer func() { unblock(); <-canceled }()
	<-entered
	tracked := make(chan error, 1)
	go func() {
		_, done, err := c.TrackInbound(2, time.Hour)
		if err == nil {
			done()
		}
		tracked <- err
	}()
	select {
	case err := <-tracked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated tracking stalled behind cancellation")
	}
	unblock()
}
