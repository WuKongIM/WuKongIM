//go:build integration

package cluster

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestNodeStartListensOnDefaultTransport(t *testing.T) {
	node, err := New(validNodeConfig(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	addr := node.transportServer.Addr()
	if addr == "" {
		t.Fatal("transport server addr is empty after Start")
	}
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial default transport addr %s: %v", addr, err)
	}
	_ = conn.Close()
}
