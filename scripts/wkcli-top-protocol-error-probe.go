//go:build ignore

package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

func main() {
	var gateway string
	var uid string
	var deviceID string
	var token string
	var timeout time.Duration
	flag.StringVar(&gateway, "gateway", "", "WKProto gateway address")
	flag.StringVar(&uid, "uid", "wkcli-top-probe", "CONNECT uid")
	flag.StringVar(&deviceID, "device", "wkcli-top-probe-device", "CONNECT device id")
	flag.StringVar(&token, "token", "", "CONNECT token")
	flag.DurationVar(&timeout, "timeout", 5*time.Second, "network operation timeout")
	flag.Parse()
	if gateway == "" {
		fmt.Fprintln(os.Stderr, "--gateway is required")
		os.Exit(2)
	}
	if err := run(gateway, uid, deviceID, token, timeout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(gateway, uid, deviceID, token string, timeout time.Duration) error {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.Dial("tcp", gateway)
	if err != nil {
		return fmt.Errorf("dial %s: %w", gateway, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}

	_, public, err := wkprotoenc.GenerateKeyPair()
	if err != nil {
		return err
	}
	proto := codec.New()
	connect := &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		ClientKey:       wkprotoenc.EncodePublicKey(public),
		DeviceID:        deviceID,
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
		UID:             uid,
		Token:           token,
	}
	payload, err := proto.EncodeFrame(connect, frame.LatestVersion)
	if err != nil {
		return err
	}
	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("write connect: %w", err)
	}
	ackFrame, err := proto.DecodePacketWithConn(conn, frame.LatestVersion)
	if err != nil {
		return fmt.Errorf("read connack: %w", err)
	}
	ack, ok := ackFrame.(*frame.ConnackPacket)
	if !ok {
		return fmt.Errorf("read connack: got %T", ackFrame)
	}
	if ack.ReasonCode != frame.ReasonSuccess {
		return fmt.Errorf("connack reason: %s", ack.ReasonCode.String())
	}

	// CONNECT fixed header, remaining length 1, then only Version. The frame is
	// complete enough to enter decodeConnect but too short for DeviceFlag.
	if _, err := conn.Write([]byte{0x10, 0x01, frame.LatestVersion}); err != nil {
		return fmt.Errorf("write malformed frame: %w", err)
	}
	time.Sleep(200 * time.Millisecond)
	return nil
}
