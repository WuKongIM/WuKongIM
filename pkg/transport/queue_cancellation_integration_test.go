//go:build integration

package transport_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/transport/testkit"
)

// Failed unbudgeted admissions still finish connection tracking. Reusing the
// connection for repeated rejects and a valid request checks the external path.
func TestUnbudgetedQueueListenerAdmissionFailure(t *testing.T) {
	server, err := transport.NewServer(transport.ServerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	if err := server.Handle(1, func(_ context.Context, p []byte) ([]byte, error) { return p, nil }, transport.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024, MaxPayload: 4}); err != nil {
		t.Fatal(err)
	}
	if err := server.ListenAndServe("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	client, err := transport.NewClient(transport.ClientConfig{RequestBudgets: false, PoolSize: 1, Discovery: testkit.StaticDiscovery{2: server.Addr()}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := 0; i < 32; i++ {
		_, err := client.Call(ctx, 2, 0, transport.PriorityRPC, 1, []byte("oversized"))
		var remote transport.RemoteError
		if !errors.As(err, &remote) || remote.Message != transport.ErrMsgTooLarge.Error() {
			t.Fatalf("admission rejection: %v", err)
		}
	}
	got, err := client.Call(ctx, 2, 0, transport.PriorityRPC, 1, []byte("ok"))
	if err != nil || string(got) != "ok" {
		t.Fatalf("valid request after rejected admissions: %q %v", got, err)
	}
}
