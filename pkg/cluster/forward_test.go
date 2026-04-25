package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestForwardToLeader_RoundTrip(t *testing.T) {
	// Server echoes the forward payload back with errCodeOK
	srv := transport.NewServer()
	mux := transport.NewRPCMux()
	mux.Handle(rpcServiceForward, func(ctx context.Context, body []byte) ([]byte, error) {
		slotID, cmd, err := decodeForwardPayload(body)
		if err != nil {
			return nil, err
		}
		_ = slotID
		return encodeForwardResp(errCodeOK, cmd), nil
	})
	srv.HandleRPCMux(mux)
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	d := NewStaticDiscovery([]NodeConfig{{NodeID: 2, Addr: srv.Listener().Addr().String()}})
	pool := transport.NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := transport.NewClient(pool)
	defer client.Stop()

	c := &Cluster{transportResources: transportResources{fwdClient: client}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := c.forwardToLeader(ctx, 2, 1, []byte("test-cmd"))
	if err != nil {
		t.Fatalf("forwardToLeader: %v", err)
	}
}

func TestForwardToLeader_NotLeader(t *testing.T) {
	srv := transport.NewServer()
	mux := transport.NewRPCMux()
	mux.Handle(rpcServiceForward, func(ctx context.Context, body []byte) ([]byte, error) {
		return encodeForwardResp(errCodeNotLeader, nil), nil
	})
	srv.HandleRPCMux(mux)
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	d := NewStaticDiscovery([]NodeConfig{{NodeID: 2, Addr: srv.Listener().Addr().String()}})
	pool := transport.NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := transport.NewClient(pool)
	defer client.Stop()

	c := &Cluster{transportResources: transportResources{fwdClient: client}}

	ctx := context.Background()
	err := c.forwardToLeader(ctx, 2, 1, []byte("test"))
	if err != ErrNotLeader {
		t.Fatalf("expected ErrNotLeader, got: %v", err)
	}
}
