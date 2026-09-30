//go:build e2e

package medium_recipient_hotpath

import (
	"bytes"
	"errors"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"
)

// These contracts precede the diagnostic implementation. The probe must not
// change pacing, payloads, errors, or acceptance, and must retain bounded state.
func TestPermissionSoakDriverWindowAndFreeze(t *testing.T) {
	p := &permissionSoakDriverProbe{}
	start := time.Unix(100, 0)
	p.observeWrite(0, start, start.Add(time.Millisecond), 100, nil)
	p.start(start)
	// A handshake that started before measurement must remain outside it.
	p.observeWrite(0, start.Add(-time.Millisecond), start.Add(time.Millisecond), 100, nil)
	p.observeEnqueue(0, start, start.Add(3*time.Millisecond), start.Add(5*time.Millisecond), nil)
	p.observeEnqueue(1, start.Add(10*time.Millisecond), start.Add(8*time.Millisecond), start.Add(9*time.Millisecond), errors.New("closed"))
	p.observeWrite(0, start.Add(5*time.Millisecond), start.Add(12*time.Millisecond), 40, errors.New("partial write"))
	p.observeWrite(1, start.Add(10*time.Millisecond), start.Add(11*time.Millisecond), 60, nil)
	got := p.freeze(start.Add(20 * time.Millisecond))
	if got.SendCalls != 2 || got.SendErrors != 1 || got.WriteCalls != 2 || got.WriteBytes != 100 || got.WriteErrors != 1 {
		t.Fatalf("wrong measured totals: %+v", got)
	}
	if got.MaxScheduleLagMS != 3 || got.MaxEnqueueMS != 2 || got.MaxWriteMS != 7 {
		t.Fatalf("wrong timing attribution: %+v", got)
	}
	if len(got.Recent) != 1 || got.Recent[0].MaxSenderSendCalls != 1 || got.Recent[0].MaxSenderWriteBytes != 60 {
		t.Fatalf("wrong per-sender burst attribution: %+v", got.Recent)
	}
	p.observeEnqueue(0, start, start.Add(time.Second), start.Add(2*time.Second), nil)
	p.observeWrite(0, start.Add(time.Second), start.Add(2*time.Second), 999, nil)
	if later := p.freeze(start.Add(3 * time.Second)); !reflect.DeepEqual(got, later) {
		t.Fatal("a frozen diagnostic changed during failure reporting or cleanup")
	}
}

func TestPermissionSoakDriverKeepsOnlyRecentBoundedBuckets(t *testing.T) {
	p := &permissionSoakDriverProbe{}
	start := time.Unix(100, 0)
	p.start(start)
	for i := 0; i < 130; i++ {
		at := start.Add(time.Duration(i) * 100 * time.Millisecond)
		p.observeEnqueue(0, at, at, at, nil)
	}
	// An old event acquiring the lock late must not replace a newer ring slot.
	p.observeWrite(0, start, start.Add(time.Millisecond), 10, nil)
	got := p.freeze(start.Add(12900 * time.Millisecond))
	if got.SendCalls != 130 || got.WriteCalls != 1 || len(got.Recent) != 100 {
		t.Fatalf("wrong retained bounds or cumulative counters: %+v", got)
	}
	if got.Recent[0].OffsetMS != 3000 || got.Recent[99].OffsetMS != 12900 {
		t.Fatalf("wrong ring order: first=%+v last=%+v", got.Recent[0], got.Recent[99])
	}
	for _, bin := range got.Recent {
		if bin.SendCalls != 1 || bin.WriteCalls != 0 {
			t.Fatalf("stale event corrupted ring: %+v", bin)
		}
	}
}

func TestPermissionSoakDriverIdleTailExpires(t *testing.T) {
	p := &permissionSoakDriverProbe{}
	start := time.Unix(100, 0)
	p.start(start)
	p.observeWrite(0, start, start.Add(time.Millisecond), 20, nil)
	got := p.freeze(start.Add(20 * time.Second))
	if got.WriteCalls != 1 || got.WriteBytes != 20 || len(got.Recent) != 0 {
		t.Fatalf("expired buckets must not look like recent traffic: %+v", got)
	}
}

func TestPermissionSoakDriverConcurrentWritersKeepExactCounters(t *testing.T) {
	p := &permissionSoakDriverProbe{}
	start := time.Unix(100, 0)
	p.start(start)
	var wg sync.WaitGroup
	for sender := 0; sender < mediumSenderConnections; sender++ {
		wg.Add(1)
		go func(sender int) {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				p.observeWrite(sender, start, start.Add(time.Millisecond), 7, nil)
			}
		}(sender)
	}
	wg.Wait()
	got := p.freeze(start.Add(2 * time.Millisecond))
	if got.WriteCalls != 2500 || got.WriteBytes != 17500 || len(got.Recent) != 1 || got.Recent[0].MaxSenderWriteBytes != 700 {
		t.Fatalf("concurrent observations lost counts: %+v", got)
	}
}

func TestPermissionSoakDriverSocketAdapterPreservesPartialWrite(t *testing.T) {
	p := &permissionSoakDriverProbe{}
	start := time.Unix(100, 0)
	p.start(start)
	wantErr := errors.New("socket failed")
	inner := &permissionDriverStubConn{n: 3, err: wantErr}
	clockCalls := 0
	conn := &permissionSoakDriverConn{Conn: inner, probe: p, sender: 2, now: func() time.Time {
		clockCalls++
		return start.Add(time.Duration(clockCalls) * time.Millisecond)
	}}
	payload := []byte("secret-payload")
	n, err := conn.Write(payload)
	if n != 3 || err != wantErr || !bytes.Equal(inner.written, payload) || !bytes.Equal(payload, []byte("secret-payload")) {
		t.Fatal("socket probe changed write bytes, result, or caller buffer")
	}
	got := p.freeze(start.Add(3 * time.Millisecond))
	if got.WriteCalls != 1 || got.WriteBytes != 3 || got.WriteErrors != 1 || got.MaxWriteMS != 1 {
		t.Fatalf("partial write was counted as a whole buffer: %+v", got)
	}
}

type permissionDriverStubConn struct {
	net.Conn
	n       int
	err     error
	written []byte
}

func (c *permissionDriverStubConn) Write(data []byte) (int, error) {
	c.written = append([]byte(nil), data...)
	return c.n, c.err
}
