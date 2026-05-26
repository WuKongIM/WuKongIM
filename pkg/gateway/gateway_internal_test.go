package gateway

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/core"
	gnettransport "github.com/WuKongIM/WuKongIM/pkg/gateway/transport/gnet"
)

func TestBuildRegistryRegistersOnlyGnetTransport(t *testing.T) {
	registry, err := buildRegistry()
	if err != nil {
		t.Fatalf("buildRegistry: %v", err)
	}

	gnetFactory, err := registry.Transport(gnettransport.Name)
	if err != nil {
		t.Fatalf("Transport(%q): %v", gnettransport.Name, err)
	}
	if _, ok := gnetFactory.(*gnettransport.Factory); !ok {
		t.Fatalf("gnet transport factory type = %T, want *gnet.Factory", gnetFactory)
	}

	if _, err := registry.Transport("stdnet"); !errors.Is(err, core.ErrTransportFactoryNotFound) {
		t.Fatalf("Transport(%q) error = %v, want %v", "stdnet", err, core.ErrTransportFactoryNotFound)
	}
}

func TestBuildRegistryPassesGnetTransportOptions(t *testing.T) {
	registry, err := buildRegistry(TransportOptions{
		Gnet: GnetTransportOptions{
			NumEventLoop: 7,
		},
	})
	if err != nil {
		t.Fatalf("buildRegistry: %v", err)
	}

	transportFactory, err := registry.Transport(gnettransport.Name)
	if err != nil {
		t.Fatalf("Transport(%q): %v", gnettransport.Name, err)
	}
	gnetFactory, ok := transportFactory.(*gnettransport.Factory)
	if !ok {
		t.Fatalf("gnet transport factory type = %T, want *gnet.Factory", transportFactory)
	}
	if got := gnetFactory.Options().NumEventLoop; got != 7 {
		t.Fatalf("gnet num event loop = %d, want 7", got)
	}
}
