package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

func TestClientConnectSendsConnectPacketAndStartsLoops(t *testing.T) {
	c, serverConn := newPipeClientServerOrFatal(t, Config{Token: "cfg-token"})

	serverErr := runTestServer(func() error {
		f, err := readTestFrame(serverConn)
		if err != nil {
			return err
		}
		connect, ok := f.(*frame.ConnectPacket)
		if !ok {
			return errors.New("server read non-CONNECT frame")
		}
		if connect.UID != "uid-1" {
			return errors.New("CONNECT UID mismatch")
		}
		if connect.DeviceID != "device-1" {
			return errors.New("CONNECT DeviceID mismatch")
		}
		if connect.Token != "cfg-token" {
			return errors.New("CONNECT Token mismatch")
		}
		if connect.ClientKey == "" {
			return errors.New("CONNECT ClientKey is empty")
		}
		keys, serverKey, err := wkprotoenc.NegotiateServerSession(connect.ClientKey)
		if err != nil {
			return err
		}
		if err := writeTestFrame(serverConn, &frame.ConnackPacket{
			Framer: frame.Framer{
				FrameType:        frame.CONNACK,
				HasServerVersion: true,
			},
			ServerVersion: frame.LatestVersion,
			ReasonCode:    frame.ReasonSuccess,
			ServerKey:     serverKey,
			Salt:          string(keys.AESIV),
			NodeId:        101,
		}); err != nil {
			return err
		}
		return nil
	})

	ack, err := c.Connect(context.Background(), ConnectOptions{
		UID:        "uid-1",
		DeviceID:   "device-1",
		DeviceFlag: frame.APP,
	})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if ack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("Connect() ack reason = %s, want %s", ack.ReasonCode, frame.ReasonSuccess)
	}
	if ack.NodeId != 101 {
		t.Fatalf("Connect() ack node id = %d, want 101", ack.NodeId)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestClientConnectAcceptsUnencryptedSuccessConnack(t *testing.T) {
	c, serverConn := newPipeClientServerOrFatal(t, Config{Token: "cfg-token"})

	serverErr := runTestServer(func() error {
		if _, err := readTestFrame(serverConn); err != nil {
			return err
		}
		return writeTestFrame(serverConn, &frame.ConnackPacket{
			Framer: frame.Framer{
				FrameType:        frame.CONNACK,
				HasServerVersion: true,
			},
			ServerVersion: frame.LatestVersion,
			ReasonCode:    frame.ReasonSuccess,
			NodeId:        202,
		})
	})

	ack, err := c.Connect(context.Background(), ConnectOptions{
		UID:        "uid-1",
		DeviceID:   "device-1",
		DeviceFlag: frame.APP,
	})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if ack.NodeId != 202 {
		t.Fatalf("Connect() ack node id = %d, want 202", ack.NodeId)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestClientConnectRejectsNonSuccessConnack(t *testing.T) {
	c, serverConn := newPipeClientServerOrFatal(t, Config{Token: "cfg-token"})

	serverErr := runTestServer(func() error {
		if _, err := readTestFrame(serverConn); err != nil {
			return err
		}
		return writeTestFrame(serverConn, &frame.ConnackPacket{
			Framer: frame.Framer{
				FrameType:        frame.CONNACK,
				HasServerVersion: true,
			},
			ServerVersion: frame.LatestVersion,
			ReasonCode:    frame.ReasonAuthFail,
		})
	})

	_, err := c.Connect(context.Background(), ConnectOptions{
		UID:        "uid-1",
		DeviceID:   "device-1",
		DeviceFlag: frame.APP,
	})
	if err == nil {
		t.Fatal("Connect() error = nil, want auth failure")
	}
	if !strings.Contains(err.Error(), frame.ReasonAuthFail.String()) {
		t.Fatalf("Connect() error = %v, want %s", err, frame.ReasonAuthFail)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestClientConnectHonorsCallerContext(t *testing.T) {
	c, err := New(Config{
		Addr:   "pipe",
		Token:  "cfg-token",
		Dialer: contextErrDialer{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = c.Connect(ctx, ConnectOptions{
		UID:        "uid-1",
		DeviceID:   "device-1",
		DeviceFlag: frame.APP,
	})
	if err == nil {
		t.Fatal("Connect() error = nil, want context cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Connect() error = %v, want %v", err, context.Canceled)
	}
}

type contextErrDialer struct{}

func (contextErrDialer) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	return nil, ctx.Err()
}

func runTestServer(fn func() error) <-chan error {
	done := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- fmt.Errorf("server panic: %v", recovered)
			}
		}()
		done <- fn()
	}()
	return done
}

func newPipeClientServerOrFatal(t *testing.T, cfg Config) (*Client, net.Conn) {
	t.Helper()

	c, serverConn, err := newPipeClientServer(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		_ = serverConn.Close()
	})
	return c, serverConn
}

func TestNormalizeConfigAppliesToolingDefaults(t *testing.T) {
	cfg, err := normalizeConfig(Config{Addr: "127.0.0.1:5100"})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	if cfg.OperationTimeout != 5*time.Second {
		t.Fatalf("OperationTimeout = %s, want 5s", cfg.OperationTimeout)
	}
	if cfg.AckTimeout != 5*time.Second {
		t.Fatalf("AckTimeout = %s, want 5s", cfg.AckTimeout)
	}
	if cfg.SendQueueCapacity != 8192 {
		t.Fatalf("SendQueueCapacity = %d, want 8192", cfg.SendQueueCapacity)
	}
	if cfg.MaxInflight != 8192 {
		t.Fatalf("MaxInflight = %d, want 8192", cfg.MaxInflight)
	}
	if cfg.BatchMaxRecords != 512 {
		t.Fatalf("BatchMaxRecords = %d, want 512", cfg.BatchMaxRecords)
	}
	if cfg.BatchMaxBytes != 512*1024 {
		t.Fatalf("BatchMaxBytes = %d, want %d", cfg.BatchMaxBytes, 512*1024)
	}
	if cfg.BatchMaxWait != time.Millisecond {
		t.Fatalf("BatchMaxWait = %s, want 1ms", cfg.BatchMaxWait)
	}
	if cfg.ReadBufferSize != 4096 {
		t.Fatalf("ReadBufferSize = %d, want 4096", cfg.ReadBufferSize)
	}
	if cfg.InboundFrameBufferSize != 1024 {
		t.Fatalf("InboundFrameBufferSize = %d, want 1024", cfg.InboundFrameBufferSize)
	}
	if cfg.GenerateClientMsgNo {
		t.Fatal("GenerateClientMsgNo default = true, want false")
	}
}

func TestNormalizeConfigRequiresAddr(t *testing.T) {
	_, err := normalizeConfig(Config{})
	if !errors.Is(err, ErrMissingAddr) {
		t.Fatalf("normalizeConfig() error = %v, want %v", err, ErrMissingAddr)
	}
}
