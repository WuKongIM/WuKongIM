//go:build integration

package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/core"
	mqttadapter "github.com/WuKongIM/WuKongIM/pkg/gateway/protocol/mqtt"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gnettransport "github.com/WuKongIM/WuKongIM/pkg/gateway/transport/gnet"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type physicalCloseHandler struct {
	opened      chan gt.Context
	closing     chan gt.Context
	release     chan struct{}
	releaseOnce sync.Once
}

func (*physicalCloseHandler) OnConnect(gt.Context, any) (*gt.PacketAuthResult, error) {
	return &gt.PacketAuthResult{Accepted: true, Reply: &mqtt.Connack{}}, nil
}
func (*physicalCloseHandler) OnPacket(gt.Context, any) error       { return nil }
func (h *physicalCloseHandler) OnSessionOpen(ctx gt.Context) error { h.opened <- ctx; return nil }
func (h *physicalCloseHandler) OnSessionClose(ctx gt.Context) error {
	h.closing <- ctx
	<-h.release
	return nil
}
func (*physicalCloseHandler) OnSessionError(gt.Context, error) {}
func (*physicalCloseHandler) OnListenerError(string, error)    {}
func (h *physicalCloseHandler) unblock()                       { h.releaseOnce.Do(func() { close(h.release) }) }

func TestPhysicalTransportCloseProofTCPAndWebSocket(t *testing.T) {
	for _, network := range []string{"tcp", "websocket"} {
		t.Run(network, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			h := &physicalCloseHandler{opened: make(chan gt.Context, 1), closing: make(chan gt.Context, 1), release: make(chan struct{})}
			registry := core.NewRegistry()
			require.NoError(t, registry.RegisterTransport(gnettransport.NewFactory()))
			require.NoError(t, registry.RegisterPacketProtocol(mqttadapter.New(mqtt.Limits{})))
			server, err := core.NewServer(registry, &gt.Options{PacketHandler: h, Listeners: []gt.ListenerOptions{{
				Name: "mqtt", Network: network, Transport: "gnet", Protocol: "mqtt", Address: "127.0.0.1:0", Path: "/",
			}}})
			require.NoError(t, err)
			require.NoError(t, server.Start())
			defer func() { h.unblock(); require.NoError(t, server.Stop()) }()
			var peerClosed func() error
			if network == "tcp" {
				conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", server.ListenerAddr("mqtt"))
				require.NoError(t, err)
				defer conn.Close()
				deadline, _ := ctx.Deadline()
				require.NoError(t, conn.SetDeadline(deadline))
				_, err = conn.Write(packetConnect)
				require.NoError(t, err)
				ack := make([]byte, 5)
				_, err = io.ReadFull(conn, ack)
				require.NoError(t, err)
				require.Equal(t, []byte{0x20, 3, 0, 0, 0}, ack)
				peerClosed = func() error { _, err := conn.Read(make([]byte, 1)); return err }
			} else {
				conn, _, err := websocket.DefaultDialer.DialContext(ctx, "ws://"+server.ListenerAddr("mqtt"), nil)
				require.NoError(t, err)
				defer conn.Close()
				deadline, _ := ctx.Deadline()
				require.NoError(t, conn.SetReadDeadline(deadline))
				require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, packetConnect))
				_, ack, err := conn.ReadMessage()
				require.NoError(t, err)
				require.Equal(t, []byte{0x20, 3, 0, 0, 0}, ack)
				peerClosed = func() error { _, _, err := conn.ReadMessage(); return err }
			}
			var entry gt.Context
			select {
			case entry = <-h.opened:
			case <-ctx.Done():
				t.Fatal("session open missing")
			}
			require.NoError(t, entry.CloseTransportAndWait(ctx, gt.CloseReasonPolicyViolation))
			require.NoError(t, entry.CloseTransportAndWait(ctx, gt.CloseReasonPolicyViolation))
			require.ErrorIs(t, entry.WritePacket(&mqtt.Pingresp{}), session.ErrSessionClosed)
			err = peerClosed()
			require.Error(t, err)
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				t.Fatalf("physical close was only a timeout: %v", err)
			}
			select {
			case closed := <-h.closing:
				require.Equal(t, gt.CloseReasonPolicyViolation, closed.CloseReason)
			case <-ctx.Done():
				t.Fatal("ordinary lifecycle close missing")
			}
			// Cleanup is still blocked above; it must not be part of physical proof.
			require.NoError(t, entry.CloseTransportAndWait(ctx, gt.CloseReasonPolicyViolation))
			artifact, err := json.MarshalIndent(map[string]any{"network": network, "physical_close": true, "write_fenced": true, "cleanup_independent": true, "repeat_join": true}, "", "  ")
			require.NoError(t, err)
			path := filepath.Join(t.TempDir(), "transport-close-proof.json")
			require.NoError(t, os.WriteFile(path, artifact, 0o600))
			t.Logf("close proof: %s\n%s", path, artifact)
		})
	}
}
