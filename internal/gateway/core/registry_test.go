package core_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway/core"
	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
)

func TestRegistryRejectsDuplicateTransportFactory(t *testing.T) {
	r := core.NewRegistry()
	if err := r.RegisterTransport(testkit.NewFakeTransportFactory("fake")); err != nil {
		t.Fatalf("first register failed: %v", err)
	}
	if err := r.RegisterTransport(testkit.NewFakeTransportFactory("fake")); err == nil {
		t.Fatal("expected duplicate transport registration error")
	}
}

func TestRegistryResolvesProtocolByName(t *testing.T) {
	r := core.NewRegistry()
	adapter := testkit.NewFakeProtocol("fake-proto")
	if err := r.RegisterProtocol(adapter); err != nil {
		t.Fatalf("register protocol failed: %v", err)
	}
	got, err := r.Protocol("fake-proto")
	if err != nil || got.Name() != "fake-proto" {
		t.Fatalf("unexpected protocol lookup: adapter=%v err=%v", got, err)
	}
}
