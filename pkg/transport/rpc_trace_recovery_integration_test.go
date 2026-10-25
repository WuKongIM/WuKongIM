//go:build integration

package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

// A failed observation handshake must fail that call without poisoning later
// independent calls or replaying its business payload on a healthy connection.
func TestRPCTraceNewCallerRecoversAfterFailedProbe(t *testing.T) {
	for _, mode := range []string{"canceled_after_accept", "deadline_after_accept", "busy", "invalid_echo"} {
		t.Run(mode, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			probed := make(chan struct{})
			release := make(chan struct{})
			done := make(chan error, 1)
			joined := false
			defer func() {
				_ = ln.Close()
				select {
				case <-release:
				default:
					close(release)
				}
				if !joined {
					select {
					case <-done:
					case <-time.After(6 * time.Second):
						t.Error("trace fixture failed to join after connection cleanup")
					}
				}
			}()
			go func() {
				peer, err := ln.Accept()
				if err != nil {
					done <- err
					return
				}
				defer peer.Close()
				_ = peer.SetDeadline(time.Now().Add(5 * time.Second))
				reply := func(f wire.Frame, status byte, body []byte) error {
					return wire.WriteFrame(peer, wire.Frame{Header: wire.Header{Kind: FrameKindRPCResponse, Priority: PriorityControl, ServiceID: f.Header.ServiceID, RequestID: f.Header.RequestID}, Body: NewOwnedBuffer(append([]byte{status}, body...), nil)}, 4096)
				}
				first, err := wire.ReadFrame(peer, 4096)
				if err != nil {
					done <- err
					return
				}
				token := append([]byte(nil), first.Body.Bytes()...)
				first.Body.Release()
				if first.Header.ServiceID != wire.TraceCapabilityServiceID {
					done <- errors.New("business call preceded trace negotiation")
					return
				}
				close(probed)
				<-release
				status, body := byte(wire.ResponseOK), token
				if mode == "busy" {
					status, body = wire.ResponseBusy, nil
				} else if mode == "invalid_echo" {
					body = []byte("invalid echo")
				}
				// For cancellation/deadline the receiver already accepted this token;
				// this successful reply is deliberately later than caller completion.
				if err = reply(first, status, body); err != nil {
					done <- err
					return
				}
				for {
					f, readErr := wire.ReadFrame(peer, 4096)
					if readErr != nil {
						done <- readErr
						return
					}
					body := append([]byte(nil), f.Body.Bytes()...)
					f.Body.Release()
					if f.Header.Kind == FrameKindRPCCancel {
						continue
					}
					if f.Header.ServiceID != wire.TraceCapabilityServiceID || !bytes.Equal(body, token) {
						done <- errors.New("later caller replayed business or rebound the accepted token")
						return
					}
					if err = reply(f, wire.ResponseOK, body); err != nil {
						done <- err
						return
					}
					break
				}
				f, err := wire.ReadFrame(peer, 4096)
				if err != nil {
					done <- err
					return
				}
				body = append([]byte(nil), f.Body.Bytes()...)
				f.Body.Release()
				if f.Header.ServiceID != 1 || string(body) != "later caller" || f.Header.RequestID>>63 != 1 {
					done <- fmt.Errorf("unexpected business dispatch service=%d body=%q", f.Header.ServiceID, body)
					return
				}
				done <- reply(f, wire.ResponseOK, body)
			}()
			c, err := NewClient(ClientConfig{RPCTraceEvery: 1, PoolSize: 1, Discovery: traceDiscovery(ln.Addr().String())})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Stop()
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "deadline_after_accept" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 250*time.Millisecond)
			}
			defer cancel()
			firstResult := make(chan error, 1)
			go func() {
				_, err := c.Call(ctx, 2, 0, PriorityRPC, 1, []byte("must never dispatch"))
				firstResult <- err
			}()
			traceWait(t, probed)
			if mode == "canceled_after_accept" {
				cancel()
			}
			if mode == "busy" || mode == "invalid_echo" {
				close(release)
			}
			select {
			case firstErr := <-firstResult:
				if firstErr == nil {
					t.Fatal("failed probe dispatched the first business call")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("first caller did not finish")
			}
			if mode == "canceled_after_accept" || mode == "deadline_after_accept" {
				close(release)
			}
			second, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			got, secondErr := c.Call(second, 2, 0, PriorityRPC, 1, []byte("later caller"))
			c.Stop()
			serverErr := <-done
			joined = true
			if secondErr != nil || string(got) != "later caller" || serverErr != nil {
				t.Fatalf("healthy connection retained failed probe: second=%q err=%v server=%v", got, secondErr, serverErr)
			}
		})
	}
}
