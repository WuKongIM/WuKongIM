package client

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

func TestClientConnectSendsConnectPacketAndStartsLoops(t *testing.T) {
	c, serverConn := newPipeClientServer(t, Config{Token: "cfg-token"})

	serverErr := make(chan error, 1)
	go func() {
		f := readTestFrame(t, serverConn)
		connect, ok := f.(*frame.ConnectPacket)
		if !ok {
			serverErr <- errors.New("server read non-CONNECT frame")
			return
		}
		if connect.UID != "uid-1" {
			serverErr <- errors.New("CONNECT UID mismatch")
			return
		}
		if connect.DeviceID != "device-1" {
			serverErr <- errors.New("CONNECT DeviceID mismatch")
			return
		}
		if connect.Token != "cfg-token" {
			serverErr <- errors.New("CONNECT Token mismatch")
			return
		}
		if connect.ClientKey == "" {
			serverErr <- errors.New("CONNECT ClientKey is empty")
			return
		}
		keys, serverKey, err := wkprotoenc.NegotiateServerSession(connect.ClientKey)
		if err != nil {
			serverErr <- err
			return
		}
		writeTestFrame(t, serverConn, &frame.ConnackPacket{
			Framer: frame.Framer{
				FrameType:        frame.CONNACK,
				HasServerVersion: true,
			},
			ServerVersion: frame.LatestVersion,
			ReasonCode:    frame.ReasonSuccess,
			ServerKey:     serverKey,
			Salt:          string(keys.AESIV),
		})
		serverErr <- nil
	}()

	if err := c.Connect(ConnectOptions{
		UID:        "uid-1",
		DeviceID:   "device-1",
		DeviceFlag: frame.APP,
	}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestClientConnectRejectsNonSuccessConnack(t *testing.T) {
	c, serverConn := newPipeClientServer(t, Config{Token: "cfg-token"})

	go func() {
		_ = readTestFrame(t, serverConn)
		writeTestFrame(t, serverConn, &frame.ConnackPacket{
			Framer: frame.Framer{
				FrameType:        frame.CONNACK,
				HasServerVersion: true,
			},
			ServerVersion: frame.LatestVersion,
			ReasonCode:    frame.ReasonAuthFail,
		})
	}()

	err := c.Connect(ConnectOptions{
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
