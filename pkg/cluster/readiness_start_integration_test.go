//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"
)

func TestWaitNodeReadySucceedsForStartedSingleNodeCluster(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.Channel.TickInterval = time.Millisecond
	cfg.Control.ClusterID = "readiness-single"
	cfg.Slots.InitialSlotCount = 1
	cfg.Slots.HashSlotCount = 4
	cfg.Slots.ReplicaCount = 1
	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// This checks readiness after real Controller election and Slot bootstrap,
	// not a three-second startup SLA. Keep production election timing bounded.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if err := node.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := WaitNodeReady(ctx, node); err != nil {
		t.Fatalf("WaitNodeReady() error = %v", err)
	}
}
