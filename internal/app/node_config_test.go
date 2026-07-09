package app

import (
	"context"
	"errors"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestNodeConfigSnapshotReturnsStartupSnapshot(t *testing.T) {
	generatedAt := time.Now().Add(-time.Hour).UTC()
	app := &App{cfg: Config{
		NodeID: 2,
		StartupConfigSnapshot: managementusecase.NodeConfigSnapshot{
			GeneratedAt: generatedAt,
			NodeID:      999,
			Groups: []managementusecase.NodeConfigGroup{
				{
					ID:    "node",
					Title: "Node",
					Items: []managementusecase.NodeConfigItem{
						{Key: "WK_NODE_ID", Label: "Node ID", Value: "2"},
						{Key: "WK_MANAGER_JWT_SECRET", Label: "JWT", Value: "******", Sensitive: true, Redacted: true},
					},
				},
			},
		},
	}}

	snapshot, err := app.NodeConfigSnapshot(context.Background(), 2)
	if err != nil {
		t.Fatalf("NodeConfigSnapshot() error = %v", err)
	}
	if snapshot.NodeID != 2 || snapshot.Source != "effective_startup_config" || !snapshot.RequiresRestart {
		t.Fatalf("snapshot metadata = %#v", snapshot)
	}
	if !snapshot.GeneratedAt.After(generatedAt) {
		t.Fatalf("GeneratedAt = %s, want refreshed after %s", snapshot.GeneratedAt, generatedAt)
	}
	if !containsNodeConfigItem(snapshot, "WK_NODE_ID", "2") {
		t.Fatalf("snapshot missing WK_NODE_ID=2: %#v", snapshot.Groups)
	}
	if !containsNodeConfigItem(snapshot, "WK_MANAGER_JWT_SECRET", "******") {
		t.Fatalf("snapshot missing redacted JWT secret: %#v", snapshot.Groups)
	}
}

func TestNodeConfigSnapshotUnavailableWithoutStartupSnapshot(t *testing.T) {
	app := &App{cfg: Config{NodeID: 2}}

	_, err := app.NodeConfigSnapshot(context.Background(), 2)
	if !errors.Is(err, managementusecase.ErrNodeConfigUnavailable) {
		t.Fatalf("NodeConfigSnapshot() error = %v, want ErrNodeConfigUnavailable", err)
	}
}

func TestNodeConfigSnapshotRejectsNonLocalNode(t *testing.T) {
	app := &App{cfg: Config{NodeID: 2}}

	_, err := app.NodeConfigSnapshot(context.Background(), 3)
	if !errors.Is(err, managementusecase.ErrNodeConfigUnavailable) {
		t.Fatalf("NodeConfigSnapshot(remote node) error = %v, want ErrNodeConfigUnavailable", err)
	}
}

func containsNodeConfigItem(snapshot managementusecase.NodeConfigSnapshot, key, value string) bool {
	for _, group := range snapshot.Groups {
		for _, item := range group.Items {
			if item.Key == key && item.Value == value {
				return true
			}
		}
	}
	return false
}
