//go:build integration

package gateway_test

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/gorilla/websocket"
)

// proxyPrefaceConn models a proxy prepending exactly one header to the first
// application write. SDK frames and HTTP Upgrade remain otherwise unchanged.
type proxyPrefaceConn struct {
	net.Conn
	preface []byte
}

func (c *proxyPrefaceConn) Write(data []byte) (int, error) {
	if len(c.preface) == 0 {
		return c.Conn.Write(data)
	}
	prefix := len(c.preface)
	wire := append(append([]byte(nil), c.preface...), data...)
	c.preface = nil
	n, err := c.Conn.Write(wire)
	return max(0, n-prefix), err
}

func TestGatewayProxyProtocolAddressesBeforeAuthenticationTCPAndWebSocket(t *testing.T) {
	source := netip.MustParseAddr("2001:db8::123").As16()
	dest := netip.MustParseAddr("2001:db8::456").As16()
	v2 := append([]byte("\r\n\r\n\x00\r\nQUIT\n"), 0x21, 0x21, 0, 36)
	v2 = append(v2, source[:]...)
	v2 = append(v2, dest[:]...)
	v2 = binary.BigEndian.AppendUint16(v2, 43210)
	v2 = binary.BigEndian.AppendUint16(v2, 5100)
	for _, network := range []string{"tcp", "websocket"} {
		t.Run(network, func(t *testing.T) {
			type addresses struct{ remote, peer, local string }
			observed := make(chan addresses, 8)
			auth := gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{})
			protocol := "wkproto"
			if network == "websocket" {
				protocol = "wsmux"
			}
			gw, err := gateway.New(gateway.Options{
				Handler: testkit.NewRecordingHandler(),
				Authenticator: gateway.AuthenticatorFunc(func(ctx *gateway.Context, packet *frame.ConnectPacket) (*gateway.AuthResult, error) {
					peer, _ := ctx.Session.Value(gateway.SessionValuePeerAddr).(string)
					observed <- addresses{ctx.Session.RemoteAddr(), peer, ctx.Session.LocalAddr()}
					return auth.Authenticate(ctx, packet)
				}),
				Listeners: []gateway.ListenerOptions{{Name: "client", Network: network, Address: "127.0.0.1:0", Transport: "gnet", Protocol: protocol, ProxyProtocolTrustedCIDRs: []string{"127.0.0.1/32"}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := gw.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = gw.Stop() })
			for _, tt := range []struct {
				name    string
				preface []byte
				remote  string
			}{
				{"direct", nil, ""},
				{"v1", []byte("PROXY TCP4 203.0.113.7 10.0.0.2 54321 5100\r\n"), "203.0.113.7:54321"},
				{"v2", v2, "[2001:db8::123]:43210"},
				{"unknown", []byte("PROXY UNKNOWN\r\n"), ""},
				{"local", append([]byte("\r\n\r\n\x00\r\nQUIT\n"), 0x20, 0xff, 0, 3, 1, 2, 3), ""},
			} {
				t.Run(tt.name, func(t *testing.T) {
					var physicalPeer string
					dial := func(ctx context.Context, network, address string) (net.Conn, error) {
						conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, address)
						if err != nil {
							return nil, err
						}
						physicalPeer = conn.LocalAddr().String()
						return &proxyPrefaceConn{Conn: conn, preface: tt.preface}, nil
					}
					connect := &frame.ConnectPacket{Version: frame.LatestVersion, UID: "proxy-client", DeviceID: "device", DeviceFlag: frame.APP, ClientTimestamp: time.Now().UnixMilli()}
					var ack *frame.ConnackPacket
					if network == "tcp" {
						conn, err := dial(context.Background(), "tcp", gw.ListenerAddr("client"))
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						ack = mustConnectWKProto(t, conn, connect)
					} else {
						dialer := websocket.Dialer{NetDialContext: dial, HandshakeTimeout: time.Second}
						conn, _, err := dialer.Dial("ws://"+gw.ListenerAddr("client")+"/", nil)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						ack = mustConnectWKProtoWS(t, conn, connect)
					}
					if ack.ReasonCode != frame.ReasonSuccess {
						t.Fatalf("CONNACK=%+v", ack)
					}
					select {
					case got := <-observed:
						wantRemote := tt.remote
						if wantRemote == "" {
							wantRemote = physicalPeer
						}
						if got.remote != wantRemote || got.peer != physicalPeer || got.local != gw.ListenerAddr("client") {
							t.Fatalf("authentication saw %+v, want remote=%s peer=%s", got, wantRemote, physicalPeer)
						}
					case <-time.After(time.Second):
						t.Fatal("authentication not observed")
					}
				})
			}
		})
	}
}
