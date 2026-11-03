//go:build integration

package client

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// This structural contract compiles against clients without diagnostic metadata.
type connectEvidence interface {
	ConnectPhase() string
	ConnectKind() string
	ConnackReason() (frame.ReasonCode, bool)
	ConnectFingerprint() [16]byte
}

func TestConnectFailureEvidenceIntegration(t *testing.T) {
	tests := []struct {
		name        string
		phase       string
		kind        string
		dialErr     error
		local       string
		writeCancel bool
		readCancel  bool
		readTimeout bool
		response    frame.Frame
		wantReason  frame.ReasonCode
		reasonKnown bool
		wantError   string
	}{
		{name: "closed", phase: "local", kind: "closed", local: "closed", wantError: "client: closed"},
		{name: "fence", phase: "local", kind: "fence", local: "fence", wantError: "client: terminal fence active"},
		{name: "dial canceled", phase: "dial", kind: "canceled", dialErr: context.Canceled},
		{name: "dial deadline", phase: "dial", kind: "deadline", dialErr: context.DeadlineExceeded},
		{name: "dial timeout", phase: "dial", kind: "timeout", dialErr: connectEvidenceTimeout{}},
		{name: "dial network", phase: "dial", kind: "network", dialErr: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
		{name: "misleading text remains unknown", phase: "dial", kind: "unknown", dialErr: errors.New("secret-host context deadline exceeded")},
		{name: "write canceled", phase: "connect_write", kind: "canceled", writeCancel: true},
		{name: "read EOF", phase: "connack_read", kind: "eof"},
		{name: "read canceled", phase: "connack_read", kind: "canceled", readCancel: true},
		{name: "read operation deadline", phase: "connack_read", kind: "deadline", readTimeout: true},
		{name: "unexpected frame", phase: "frame", kind: "protocol", response: &frame.PingPacket{}},
		{name: "auth rejected", phase: "rejected", kind: "rejected", response: &frame.ConnackPacket{ReasonCode: frame.ReasonAuthFail}, wantReason: frame.ReasonAuthFail, reasonKnown: true, wantError: "client: connack reason=ReasonAuthFail"},
		{name: "system rejected", phase: "rejected", kind: "rejected", response: &frame.ConnackPacket{ReasonCode: frame.ReasonSystemError}, wantReason: frame.ReasonSystemError, reasonKnown: true},
		{name: "known unknown reason", phase: "rejected", kind: "rejected", response: &frame.ConnackPacket{ReasonCode: frame.ReasonUnknown}, wantReason: frame.ReasonUnknown, reasonKnown: true},
		{name: "unknown wire reason", phase: "rejected", kind: "rejected", response: &frame.ConnackPacket{ReasonCode: frame.ReasonCode(255)}},
		{name: "bad crypto", phase: "crypto", kind: "crypto", response: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerKey: "invalid-key", Salt: "invalid-salt"}},
		{name: "closed after connack", phase: "local", kind: "closed", local: "after", response: &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess}, wantError: "client: closed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			clientConn, serverConn := net.Pipe()
			defer clientConn.Close()
			defer serverConn.Close()
			observer := &connectEvidenceObserver{}
			var c *Client
			wrapped := &connectEvidenceConn{Conn: clientConn}
			var readStarted chan struct{}
			if test.kind == "eof" {
				readStarted = make(chan struct{})
				wrapped.beforeRead = sync.OnceFunc(func() { close(readStarted) })
			}
			if test.writeCancel {
				wrapped.beforeWrite = cancel
			}
			if test.local == "after" {
				wrapped.afterRead = func() { _ = c.Close() }
			}
			timeout := time.Second
			if test.readTimeout {
				timeout = 25 * time.Millisecond
			}
			var err error
			c, err = New(Config{Addr: "sensitive-endpoint", Observer: observer, OperationTimeout: timeout,
				Dialer: connectEvidenceDialer(func(context.Context, string, string) (net.Conn, error) {
					if test.dialErr != nil {
						return nil, test.dialErr
					}
					return wrapped, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if test.local == "closed" {
				_ = c.Close()
			} else if test.local == "fence" {
				c.terminalFence = terminalFenceAwaitingAck
			}
			var packet *frame.ConnectPacket
			serverDone := make(chan error, 1)
			releaseServer := make(chan struct{})
			if test.dialErr == nil && test.local != "closed" && test.local != "fence" && !test.writeCancel {
				go func() {
					f, readErr := readTestFrame(serverConn)
					if readErr != nil {
						serverDone <- readErr
						return
					}
					var ok bool
					packet, ok = f.(*frame.ConnectPacket)
					if !ok {
						serverDone <- errors.New("expected CONNECT")
						return
					}
					if test.readCancel {
						cancel()
					}
					if test.readTimeout || test.readCancel {
						<-releaseServer
						serverDone <- nil
						return
					}
					if test.response == nil {
						if readStarted != nil {
							// Close after read admission: closing earlier can make
							// net.Pipe.SetReadDeadline fail with ErrClosedPipe.
							select {
							case <-readStarted:
							case <-releaseServer:
								serverDone <- errors.New("EOF fixture never entered read")
								return
							}
						}
						serverDone <- serverConn.Close()
						return
					}
					serverDone <- writeTestFrame(serverConn, test.response)
				}()
			} else {
				serverDone <- nil
			}
			_, err = c.Connect(ctx, ConnectOptions{UID: "secret-user", DeviceID: "secret-device", Token: "secret-token", DeviceFlag: frame.APP})
			close(releaseServer)
			if serverErr := <-serverDone; serverErr != nil {
				t.Fatalf("wire fixture: %v", serverErr)
			}
			if err == nil {
				t.Fatal("Connect succeeded, want failure")
			}
			if test.wantError != "" && err.Error() != test.wantError {
				t.Fatalf("error text = %q, want %q", err.Error(), test.wantError)
			}
			if test.dialErr != nil && !errors.Is(err, test.dialErr) {
				t.Fatalf("original dial error family lost: %v", err)
			}
			if test.kind == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation family lost: %v", err)
			}
			var evidence connectEvidence
			if !errors.As(err, &evidence) {
				t.Fatalf("actual Connect error has no closed diagnostic metadata: %T", err)
			}
			wantKind := test.kind
			if test.readTimeout && !errors.Is(err, context.DeadlineExceeded) {
				wantKind = "timeout"
			}
			if evidence.ConnectPhase() != test.phase || evidence.ConnectKind() != wantKind {
				t.Fatalf("phase/kind = %s/%s, want %s/%s", evidence.ConnectPhase(), evidence.ConnectKind(), test.phase, wantKind)
			}
			if reason, known := evidence.ConnackReason(); reason != test.wantReason || known != test.reasonKnown {
				t.Fatalf("reason = %d/%v, want %d/%v", reason, known, test.wantReason, test.reasonKnown)
			}
			fingerprint := evidence.ConnectFingerprint()
			if packet != nil {
				if want := connectEvidenceWireFingerprint(packet); fingerprint != want {
					t.Fatalf("fingerprint does not bind actual CONNECT: %x, want %x", fingerprint, want)
				}
			} else if test.writeCancel {
				if fingerprint == ([16]byte{}) {
					t.Fatal("constructed CONNECT fingerprint is missing")
				}
			} else if fingerprint != ([16]byte{}) {
				t.Fatal("pre-packet error invented handshake fingerprint")
			}
			if observer.err != err || observer.calls != 1 {
				t.Fatalf("observer did not retain same result: calls=%d", observer.calls)
			}
			encoded, marshalErr := json.Marshal(err)
			if marshalErr != nil || strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "sensitive") {
				t.Fatalf("cause serialized: %s (%v)", encoded, marshalErr)
			}
			var original *net.OpError
			if errors.As(test.dialErr, &original) {
				var retained *net.OpError
				if !errors.As(err, &retained) || retained != original {
					t.Fatal("original net.OpError family lost")
				}
			}
		})
	}
}

// This reference computes the documented wire tuple independently of client state.
func connectEvidenceWireFingerprint(packet *frame.ConnectPacket) [16]byte {
	h := sha256.New()
	h.Write([]byte("wukongim/connect-attempt/v1\x00"))
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(packet.ClientTimestamp))
	h.Write(stamp[:])
	h.Write([]byte(packet.ClientKey))
	var result [16]byte
	copy(result[:], h.Sum(nil))
	return result
}

type connectEvidenceDialer func(context.Context, string, string) (net.Conn, error)

func (d connectEvidenceDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d(ctx, network, addr)
}

type connectEvidenceTimeout struct{}

func (connectEvidenceTimeout) Error() string   { return "secret endpoint timeout" }
func (connectEvidenceTimeout) Timeout() bool   { return true }
func (connectEvidenceTimeout) Temporary() bool { return false }

type connectEvidenceConn struct {
	net.Conn
	beforeWrite func()
	beforeRead  func()
	afterRead   func()
	readOnce    sync.Once
}

func (c *connectEvidenceConn) Write(body []byte) (int, error) {
	if c.beforeWrite != nil {
		c.beforeWrite()
	}
	return c.Conn.Write(body)
}

func (c *connectEvidenceConn) Read(body []byte) (int, error) {
	if c.beforeRead != nil {
		c.beforeRead()
	}
	n, err := c.Conn.Read(body)
	if n > 0 && c.afterRead != nil {
		c.readOnce.Do(c.afterRead)
	}
	return n, err
}

type connectEvidenceObserver struct {
	err   error
	calls int
}

func (o *connectEvidenceObserver) OnConnect(event ConnectEvent) {
	o.err, o.calls = event.Err, o.calls+1
}
func (*connectEvidenceObserver) OnSendQueue(SendQueueEvent) {}
func (*connectEvidenceObserver) OnSendBatch(SendBatchEvent) {}
func (*connectEvidenceObserver) OnSendAck(SendAckEvent)     {}
func (*connectEvidenceObserver) OnRecv(RecvEvent)           {}
func (*connectEvidenceObserver) OnError(ErrorEvent)         {}
