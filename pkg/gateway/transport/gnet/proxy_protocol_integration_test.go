//go:build integration

package gnet

import (
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
)

func startProxyTestListener(t *testing.T, trusted []string, handler transport.ConnHandler) *listenerHandle {
	t.Helper()
	spec := namedTCPListenerSpec("proxy", handler)
	spec.Options.ProxyProtocolTrustedCIDRs = trusted
	spec.Options.MaxPendingBytes = 1 << 20
	listeners, err := NewFactory().Build([]transport.ListenerSpec{spec})
	if err != nil {
		t.Fatal(err)
	}
	listener := requireListenerHandle(t, listeners[0])
	if err := listener.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Stop() })
	return listener
}

func TestProxyProtocolListenerMixesDirectV1V2AndKeepsPayload(t *testing.T) {
	handler := newTCPRecordingHandler(func(conn transport.Conn, data []byte) error { return conn.Write(data) })
	listener := startProxyTestListener(t, []string{"127.0.0.1/32"}, handler)
	for _, wire := range [][]byte{nil, []byte("PROXY TCP4 203.0.113.1 10.0.0.2 12345 5100\r\n"), proxyV2TestHeader(1, 0x21, proxyV2TestAddress(true))} {
		conn := mustDialTCP(t, listener.Addr())
		if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		payload := bytes.Repeat([]byte("payload"), 4096)
		if _, err := conn.Write(append(append([]byte(nil), wire...), payload...)); err != nil {
			t.Fatal(err)
		}
		reply := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, reply); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(reply, payload) {
			t.Fatal("coalesced bytes lost")
		}
		// A second header-shaped payload is application data, never another preface.
		writeAndReadExact(t, conn, []byte("PROXY UNKNOWN\r\n"), "PROXY UNKNOWN\r\n")
		_ = conn.Close()
	}
	waitUntil(t, time.Second, func() bool { return handler.CloseCount() == 3 })
	if handler.OpenCount() != 3 {
		t.Fatalf("opens=%d", handler.OpenCount())
	}
	for i, want := range []string{"127.0.0.1:", "203.0.113.1:12345", "[2001:db8::1]:12345"} {
		if !strings.HasPrefix(handler.OpenSnapshot(i).remoteAddr, want) {
			t.Fatalf("open %d = %+v", i, handler.OpenSnapshot(i))
		}
	}
}

func TestProxyProtocolFragmentedStatePreservesBytesAndOpenOrdering(t *testing.T) {
	prefaces := [][]byte{[]byte("PROXY TCP4 203.0.113.1 10.0.0.2 12345 5100\r\n"), proxyV2TestHeader(1, 0x21, proxyV2TestAddress(true)), []byte("PROXimate plain traffic")}
	prefixes, _ := gatewaytypes.ParseProxyProtocolTrustedCIDRs([]string{"127.0.0.1/32"})
	for _, preface := range prefaces {
		for split := 1; split < len(preface); split++ {
			raw := &contractGnetConn{}
			state := &connState{raw: raw, runtime: &listenerRuntime{opts: transport.ListenerOptions{Network: "tcp"}, trustedProxies: prefixes}, peerAddr: "127.0.0.1:1234", remoteAddr: "127.0.0.1:1234"}
			state.startProxyHandshake(time.Minute)
			first, rest, ready := state.consumeProxyHandshake(preface[:split])
			var got []byte
			if ready {
				got = append(append(got, first...), rest...)
				got = append(got, preface[split:]...)
			} else {
				if len(state.queue) != 0 {
					t.Fatal("opened before complete preface")
				}
				payload := append(append([]byte(nil), preface[split:]...), bytes.Repeat([]byte{'z'}, 8192)...)
				first, rest, ready = state.consumeProxyHandshake(payload)
				if !ready {
					t.Fatalf("split %d did not complete", split)
				}
				got = append(append(got, first...), rest...)
			}
			if state.proxy != nil {
				t.Fatal("preface state retained")
			}
			if len(state.queue) != 1 || state.queue[0].kind != connEventOpen {
				t.Fatal("OnOpen not queued exactly once")
			}
			if bytes.HasPrefix(preface, []byte("PROXimate")) {
				if !bytes.HasPrefix(got, preface) {
					t.Fatal("partial plain signature lost")
				}
			} else if !bytes.Equal(got, bytes.Repeat([]byte{'z'}, 8192)) {
				t.Fatalf("split %d lost business bytes", split)
			}
		}
	}
}

func TestProxyProtocolRejectsUntrustedAndMalformedBeforeOpen(t *testing.T) {
	for _, tt := range []struct {
		name    string
		trusted []string
		wire    []byte
	}{
		{"untrusted v1", []string{"192.0.2.0/24"}, []byte("PROXY UNKNOWN\r\n")},
		{"untrusted v2", []string{"192.0.2.0/24"}, proxyV2TestHeader(0, 0, nil)},
		{"invalid v1", []string{"127.0.0.1/32"}, []byte("PROXY TCP4 invalid 10.0.0.2 1 2\r\n")},
		{"oversize v2", []string{"127.0.0.1/32"}, proxyV2TestHeader(1, 0, make([]byte, proxyMaxHeaderBytes))[:16]},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := newTCPRecordingHandler(func(conn transport.Conn, data []byte) error { return conn.Write(data) })
			listener := startProxyTestListener(t, tt.trusted, handler)
			conn := mustDialTCP(t, listener.Addr())
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			_, _ = conn.Write(tt.wire)
			var buf [1]byte
			_, err := conn.Read(buf[:])
			if err == nil {
				t.Fatal("accepted invalid preface")
			}
			if e, ok := err.(net.Error); ok && e.Timeout() {
				t.Fatalf("did not close: %v", err)
			}
			if handler.OpenCount() != 0 || handler.DataCount() != 0 || handler.CloseCount() != 0 {
				t.Fatal("rejected preface reached handler")
			}
			// The same listener must still admit an ordinary direct client.
			direct := mustDialTCP(t, listener.Addr())
			defer direct.Close()
			writeAndReadExact(t, direct, []byte("hello"), "hello")
		})
	}
}

func TestProxyProtocolAbsoluteTimeoutAndEarlyClose(t *testing.T) {
	handler := newTCPRecordingHandler(nil)
	listener := startProxyTestListener(t, []string{"127.0.0.1/32"}, handler)
	conn := mustDialTCP(t, listener.Addr())
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(proxyHeaderTimeout + 2*time.Second))
	_, _ = conn.Write([]byte("PRO"))
	var buf [1]byte
	_, err := conn.Read(buf[:])
	if err == nil {
		t.Fatal("partial header remained open")
	}
	if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatalf("transport did not enforce timeout: %v", err)
	}
	if handler.OpenCount() != 0 || handler.DataCount() != 0 || handler.CloseCount() != 0 {
		t.Fatal("timed out preface reached handler")
	}
	for i := 0; i < 10; i++ {
		c := mustDialTCP(t, listener.Addr())
		_, _ = c.Write([]byte("PRO"))
		_ = c.Close()
	}
	if err := listener.Stop(); err != nil {
		t.Fatal(err)
	}
	if handler.OpenCount() != 0 {
		t.Fatal("early close opened session")
	}
}
