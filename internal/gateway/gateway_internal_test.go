package gateway

import (
	"testing"

	gnettransport "github.com/WuKongIM/WuKongIM/internal/gateway/transport/gnet"
	"github.com/WuKongIM/WuKongIM/internal/gateway/transport/stdnet"
)

func TestNewRegistersRealTransportFactories(t *testing.T) {
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

	stdFactory, err := registry.Transport(stdnet.Name)
	if err != nil {
		t.Fatalf("Transport(%q): %v", stdnet.Name, err)
	}
	if _, ok := stdFactory.(*stdnet.Factory); !ok {
		t.Fatalf("stdnet transport factory type = %T, want *stdnet.Factory", stdFactory)
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
