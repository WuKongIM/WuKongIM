//go:build integration

package chatlifecycle

import (
	"context"
	"fmt"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Failure cases: owner-local group pairs collapse onto one product gateway;
// only one worker/category is covered; changing an assignment mutates a live
// factory's route; or credential preparation/cancellation is bypassed. Exercise
// the real default worker factory, token HTTP API and SDK CONNECT destination.
// Protocol peers are bounded test adapters, not product cluster acceptance.
func TestDefaultWorkerGroupRosterPreservesMemberGatewayPlacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	type arrival struct {
		node int
		uid  string
	}
	arrivals := make(chan arrival, 64)
	peerErrors := make(chan error, 64)
	stop := make(chan struct{})
	var accepts, handlers sync.WaitGroup
	listeners := make([]net.Listener, 0, 3)
	clients := make([]SessionClient, 0, 32)
	defer func() {
		close(stop)
		for _, l := range listeners {
			_ = l.Close()
		}
		for _, c := range clients {
			_ = c.Close()
		}
		accepts.Wait()
		handlers.Wait()
	}()
	gateways := make([]string, 0, 3)
	for node := 0; node < 3; node++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, l)
		gateways = append(gateways, l.Addr().String())
		accepts.Add(1)
		go func(node int, l net.Listener) {
			defer accepts.Done()
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				handlers.Add(1)
				go func() {
					defer handlers.Done()
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
					packet, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
					if err != nil {
						peerErrors <- err
						return
					}
					connect, ok := packet.(*frame.ConnectPacket)
					if !ok {
						peerErrors <- fmt.Errorf("expected CONNECT")
						return
					}
					select {
					case arrivals <- arrival{node, connect.UID}:
					case <-ctx.Done():
						return
					}
					if err := writeEngineAttemptFrame(conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion}); err != nil {
						peerErrors <- err
						return
					}
					<-stop
				}()
			}
		}(node, l)
	}
	var tokenCalls atomic.Int64
	apis := make([]*httptest.Server, 0, 3)
	addresses := make([]string, 0, 3)
	defer func() {
		for _, s := range apis {
			s.Close()
		}
	}()
	for range 3 {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/bench/v1/users/tokens" || r.Header.Get("Authorization") != "Bearer gateway-test" {
				http.Error(w, "unexpected request", 400)
				return
			}
			tokenCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"accepted":1}`))
		}))
		apis = append(apis, s)
		addresses = append(addresses, s.URL)
	}
	cfg := FormalConfig()
	cfg.Profile = ProfileLocal
	cfg.Stage = LocalConfig().Stage
	cfg.Workload.OnlineUsers = 5000
	cfg.Workload.SendRatePerSecond = 1000
	cfg.Workload.MaxGlobalBurst = 2000
	cfg.Observation.APIAddrs = addresses
	cfg.Observation.GatewayTCPAddrs = gateways
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	connections := 0
	for worker := uint64(0); worker < 3; worker++ {
		generation, err := NewEngineWorkerGenerationFactory("gateway-test").New(WorkerAssignment{WorkerFence: WorkerFence{RunID: cfg.RunID, AssignmentID: fmt.Sprintf("gateway-worker-%d", worker), Generation: 1}, WorkerID: worker, WorkerCount: 3, Config: cfg})
		if err != nil {
			t.Fatal(err)
		}
		g := generation.(*engineWorkerGeneration)
		factory := g.engine.sessions.factory
		catalog := g.engine.generator.catalog
		// A live generation owns its endpoint configuration even if the caller
		// later reuses the assignment's slice. The factory must retain its copy.
		old := cfg.Observation.GatewayTCPAddrs[0]
		cfg.Observation.GatewayTCPAddrs[0] = "127.0.0.1:1"
		for category := GroupSmall; category <= GroupVeryLarge; category++ {
			start, count, ok := catalog.categoryRange(category)
			if !ok {
				continue
			}
			var group Group
			found := false
			for index := start; index < start+uint64(count); index++ {
				owner, _ := catalog.GroupOwner(index)
				if owner == worker {
					group, err = catalog.Group(index)
					if err != nil {
						t.Fatal(err)
					}
					found = true
					break
				}
			}
			if !found {
				continue
			}
			for ordinal := 0; ordinal < 3; ordinal++ {
				userIndex, err := group.MemberIndex(ordinal)
				if err != nil {
					t.Fatal(err)
				}
				owner, _ := g.engine.sessions.identity.Owner(userIndex)
				if owner != worker {
					t.Fatal("fixture lost owner-local roster")
				}
				uid := g.engine.sessions.identity.UID(userIndex)
				client, err := factory.NewSession(ctx, uid, "gateway-connect-token")
				if err != nil {
					t.Fatal(err)
				}
				clients = append(clients, client)
				if err := client.Connect(ctx, uid, "gateway-test-device"); err != nil {
					t.Fatal(err)
				}
				select {
				case got := <-arrivals:
					// Preserve the product gateway used by this ordinal in the original
					// unaligned catalog, independently of the new driver-owned identity.
					want := int((group.Index + uint64(ordinal)*uint64(catalog.Count())) % 3)
					if got.uid != uid || got.node != want {
						t.Errorf("worker=%d category=%d ordinal=%d actual_gateway=%d expected_original_gateway=%d", worker, category, ordinal, got.node, want)
					}
				case err := <-peerErrors:
					t.Fatal(err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				connections++
			}
		}
		// An identity beyond every bounded fixed roster retains this
		// driver's original gateway instead of being remapped as a member.
		uid := g.engine.sessions.identity.UID(300_000_000 + worker)
		client, err := factory.NewSession(ctx, uid, "gateway-connect-token")
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, client)
		if err := client.Connect(ctx, uid, "gateway-test-device"); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-arrivals:
			if got.uid != uid || got.node != int(worker) {
				t.Errorf("non-group worker=%d actual_gateway=%d", worker, got.node)
			}
		case err := <-peerErrors:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		connections++
		cfg.Observation.GatewayTCPAddrs[0] = old
	}
	if connections != 33 || tokenCalls.Load() != int64(connections) {
		t.Fatalf("CONNECT/token calls=%d/%d want33/33", connections, tokenCalls.Load())
	}
}
