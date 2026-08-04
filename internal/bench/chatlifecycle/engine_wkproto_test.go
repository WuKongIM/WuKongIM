package chatlifecycle

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type engineRealWKFactory struct {
	addr       string
	ackTimeout time.Duration
}

func (f engineRealWKFactory) NewSession(context.Context, string, string) (SessionClient, error) {
	client, err := wkproto.NewClient(wkproto.ClientConfig{
		Addr: f.addr, OperationTimeout: time.Second, AckTimeout: f.ackTimeout,
		SendQueueCapacity: 4, MaxInflight: 4, FrameBufferSize: 4,
	})
	if err != nil {
		return nil, err
	}
	return NewWKProtoSessionAdapter(client)
}

func TestEngineRealWKProtoOverlappingAttemptsAcceptEitherAckOrder(t *testing.T) {
	for _, order := range []struct {
		name  string
		first int
	}{
		{name: "older_attempt_first", first: 0},
		{name: "current_attempt_first", first: 1},
	} {
		t.Run(order.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("Listen: %v", err)
			}
			defer listener.Close()
			attemptsReady := make(chan [2]*frame.SendPacket, 1)
			writeOrder := make(chan int, 1)
			serverErr := make(chan error, 1)
			serverStop := make(chan struct{})
			go runEngineAttemptServer(listener, attemptsReady, writeOrder, serverStop, serverErr)

			fixture := newEngineTestFixture(t, engineTestLimits{
				OnlineUsers: 1, AttemptTimeout: time.Millisecond,
			})
			fixture.pool.factory = engineRealWKFactory{addr: listener.Addr().String(), ackTimeout: time.Second}
			if err := fixture.engine.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer close(serverStop)
			defer fixture.engine.Stop()

			sender := fixture.identity.UID(0)
			if _, err := fixture.engine.Login(context.Background(), SessionLogin{
				UID: sender, UserIndex: 0, LoginOrdinal: 0,
			}); err != nil {
				t.Fatalf("Login: %v", err)
			}
			intent := fixture.intent(t, sender, "real-overlap-group", 149, TrafficGroup)
			now := fixture.clock.Now()
			if err := fixture.engine.SubmitGranted(intent, now); err != nil {
				t.Fatalf("SubmitGranted: %v", err)
			}
			if _, err := fixture.engine.Advance(now); err != nil {
				t.Fatalf("attempt zero Advance: %v", err)
			}
			now = now.Add(time.Millisecond)
			fixture.clock.Set(now)
			if _, err := fixture.engine.Advance(now); err != nil {
				t.Fatalf("attempt zero timeout Advance: %v", err)
			}
			retry, err := fixture.retry.Attempt(intent.Logical, 1)
			if err != nil {
				t.Fatalf("Attempt(1): %v", err)
			}
			now = now.Add(retry.Delay)
			fixture.clock.Set(now)
			if _, err := fixture.engine.Advance(now); err != nil {
				t.Fatalf("attempt one Advance: %v", err)
			}

			var attempts [2]*frame.SendPacket
			select {
			case attempts = <-attemptsReady:
			case err := <-serverErr:
				t.Fatalf("server before attempts: %v", err)
			case <-time.After(time.Second):
				t.Fatal("server did not receive two overlapping attempts")
			}
			if attempts[0].ClientMsgNo != attempts[1].ClientMsgNo || attempts[0].ClientSeq == attempts[1].ClientSeq {
				t.Fatalf("real TCP attempts = %+v", attempts)
			}
			writeOrder <- order.first

			completed := false
			for spin := 0; spin < 10_000; spin++ {
				snapshot, snapshotErr := fixture.engine.Snapshot()
				if snapshotErr != nil {
					t.Fatalf("Snapshot: %v", snapshotErr)
				}
				verifier := fixture.verifier.Snapshot()
				if snapshot.InflightCurrent == 0 && verifier.ReleasedAttemptCurrent == 0 && verifier.Acknowledged == 1 {
					completed = true
					break
				}
				runtime.Gosched()
			}
			if !completed {
				t.Fatalf("overlapping attempts did not settle: engine=%+v verifier=%+v", mustEngineSnapshot(t, fixture.engine), fixture.verifier.Snapshot())
			}
			verifier := fixture.verifier.Snapshot()
			if verifier.UnknownSendacks != 0 || verifier.DuplicateCompletions != 0 || verifier.ConflictingCompletions != 0 {
				t.Fatalf("real TCP attempt ownership evidence = %+v", verifier)
			}
			select {
			case err := <-serverErr:
				if err != nil {
					t.Fatalf("server: %v", err)
				}
			default:
			}
		})
	}
}

func runEngineAttemptServer(
	listener net.Listener,
	attemptsReady chan<- [2]*frame.SendPacket,
	writeOrder <-chan int,
	stop <-chan struct{},
	result chan<- error,
) {
	conn, err := listener.Accept()
	if err != nil {
		result <- err
		return
	}
	defer conn.Close()
	protocol := codec.New()
	if _, err := protocol.DecodePacketWithConn(conn, frame.LatestVersion); err != nil {
		result <- fmt.Errorf("decode CONNECT: %w", err)
		return
	}
	if err := writeEngineAttemptFrame(conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion}); err != nil {
		result <- fmt.Errorf("write CONNACK: %w", err)
		return
	}
	var attempts [2]*frame.SendPacket
	for index := range attempts {
		packet, err := protocol.DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			result <- fmt.Errorf("decode SEND %d: %w", index, err)
			return
		}
		attempts[index] = packet.(*frame.SendPacket)
	}
	attemptsReady <- attempts
	first := <-writeOrder
	for _, index := range []int{first, 1 - first} {
		send := attempts[index]
		if err := writeEngineAttemptFrame(conn, &frame.SendackPacket{
			ClientSeq: send.ClientSeq, ClientMsgNo: send.ClientMsgNo,
			MessageID: 901, MessageSeq: 902, ReasonCode: frame.ReasonSuccess,
		}); err != nil {
			result <- fmt.Errorf("write SENDACK %d: %w", index, err)
			return
		}
	}
	<-stop
	result <- nil
}

func writeEngineAttemptFrame(conn net.Conn, packet frame.Frame) error {
	payload, err := codec.New().EncodeFrame(packet, frame.LatestVersion)
	if err != nil {
		return err
	}
	_, err = conn.Write(payload)
	return err
}

func mustEngineSnapshot(t *testing.T, engine *Engine) EngineSnapshot {
	t.Helper()
	snapshot, err := engine.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return snapshot
}
