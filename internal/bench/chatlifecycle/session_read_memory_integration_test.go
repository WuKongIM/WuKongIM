//go:build integration

package chatlifecycle

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// Measure live connections created by the production factory, not the size of
// a configuration struct. A received frame proves each actual reader started.
func TestWorkerSessionConnectedMemoryBudget(t *testing.T) {
	const sessions = 128
	const bytesPerSessionBudget = 64 << 10
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	acceptDone := make(chan struct{})
	serverErrors := make(chan error, sessions+1)
	var handlers sync.WaitGroup
	clients := make([]SessionClient, 0, sessions)
	defer func() {
		close(stop)
		_ = listener.Close()
		for _, c := range clients {
			_ = c.Close()
		}
		<-acceptDone
		handlers.Wait()
	}()
	go func() {
		defer close(acceptDone)
		for range sessions {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
				protocol := codec.New()
				if _, err := protocol.DecodePacketWithConn(conn, frame.LatestVersion); err != nil {
					serverErrors <- err
					return
				}
				for _, packet := range []frame.Frame{
					&frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion},
					&frame.RecvPacket{Setting: frame.SettingNoEncrypt, MessageID: 1, MessageSeq: 1, ClientMsgNo: "memory-ready", ChannelID: "memory-ready", ChannelType: 2, Payload: []byte("ready")},
				} {
					if err := writeEngineAttemptFrame(conn, packet); err != nil {
						serverErrors <- err
						return
					}
				}
				<-stop
			}()
		}
	}()
	factory := engineWorkerSessionFactory{
		address: listener.Addr().String(), ackTimeout: 2 * time.Second, runID: "memory-run", assignmentID: "memory-worker",
		tokens: target.NewClient(target.Config{APIAddrs: []string{"http://memory-target.test"}, HTTPClient: &http.Client{Transport: workerRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"accepted":1}`)), Header: make(http.Header)}, nil
		})}}),
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := range sessions {
		uid := fmt.Sprintf("memory-user-%d", i)
		client, err := factory.NewSession(ctx, uid, "memory-token")
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, client)
		if err := client.Connect(ctx, uid, "memory-device"); err != nil {
			t.Fatal(err)
		}
		packet, err := client.ReadFrame(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if recv, ok := packet.(*frame.RecvPacket); !ok || string(recv.Payload) != "ready" {
			t.Fatal("reader readiness packet mismatch")
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(clients)
	select {
	case err := <-serverErrors:
		t.Fatalf("memory peer failed: %v", err)
	default:
	}
	var retained uint64
	if after.HeapAlloc > before.HeapAlloc {
		retained = after.HeapAlloc - before.HeapAlloc
	}
	const budget = sessions * bytesPerSessionBudget
	t.Logf("connected_sessions=%d retained_heap_bytes=%d budget_bytes=%d", sessions, retained, budget)
	if retained > budget {
		t.Fatalf("connected session heap = %d bytes; budget %d", retained, budget)
	}
}
