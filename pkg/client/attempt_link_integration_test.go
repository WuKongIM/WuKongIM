//go:build integration

package client

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

// F05/F06: only an opted-in successful actual CONNECT can publish a digest;
// credentials and reconstructed timestamps are never evidence inputs.
func TestAttemptLinkSuccessRetainsActualConnectTupleOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			c, peer := newPipeClientServerOrFatal(t, Config{EnableAttemptLinkDiagnostics: enabled})
			var packet *frame.ConnectPacket
			done := runTestServer(func() error {
				f, err := readTestFrame(peer)
				if err != nil {
					return err
				}
				var ok bool
				packet, ok = f.(*frame.ConnectPacket)
				if !ok {
					return errors.New("expected actual CONNECT")
				}
				keys, key, err := wkprotoenc.NegotiateServerSession(packet.ClientKey)
				if err != nil {
					return err
				}
				return writeTestFrame(peer, &frame.ConnackPacket{Framer: frame.Framer{FrameType: frame.CONNACK, HasServerVersion: true}, ServerVersion: frame.LatestVersion, ReasonCode: frame.ReasonSuccess, ServerKey: key, Salt: string(keys.AESIV)})
			})
			if got := c.ConnectFingerprint(); got != [16]byte{} {
				t.Fatal("pre-connect identity invented")
			}
			if _, err := c.Connect(context.Background(), ConnectOptions{UID: "private-user", DeviceID: "private-device", Token: "private-token", DeviceFlag: frame.APP}); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			got := c.ConnectFingerprint()
			if enabled {
				if want := connectEvidenceWireFingerprint(packet); got != want || got == [16]byte{} {
					t.Fatalf("not actual packet: %x/%x", got, want)
				}
			} else if got != [16]byte{} {
				t.Fatal("default-off retained successful CONNECT evidence")
			}
			_ = c.Close()
			if c.ConnectFingerprint() != [16]byte{} {
				t.Fatal("closed connection advertised active scope")
			}
		})
	}
}
func TestAttemptLinkRejectedConnectionDoesNotPublishSuccessfulScope(t *testing.T) {
	c, peer := newPipeClientServerOrFatal(t, Config{EnableAttemptLinkDiagnostics: true})
	done := runTestServer(func() error {
		if _, err := readTestFrame(peer); err != nil {
			return err
		}
		return writeTestFrame(peer, &frame.ConnackPacket{ReasonCode: frame.ReasonAuthFail})
	})
	_, err := c.Connect(context.Background(), ConnectOptions{UID: "private-user", DeviceID: "private-device"})
	if err == nil {
		t.Fatal("expected rejection")
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if c.ConnectFingerprint() != [16]byte{} {
		t.Fatal("failed authentication became a successful connection")
	}
}
