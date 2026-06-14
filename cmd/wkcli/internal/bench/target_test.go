package bench

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	contextcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/context"
)

func TestResolveTargetUsesManualGatewaysWithoutHTTP(t *testing.T) {
	target, err := resolveTarget(context.Background(), sendConfig{
		GatewayAddrs: []string{"127.0.0.1:5100, 127.0.0.1:5101"},
	})

	if err != nil {
		t.Fatalf("resolveTarget() error = %v", err)
	}
	if want := []string{"127.0.0.1:5100", "127.0.0.1:5101"}; !reflect.DeepEqual(target.GatewayAddrs, want) {
		t.Fatalf("GatewayAddrs = %#v, want %#v", target.GatewayAddrs, want)
	}
}

func TestResolveTargetDiscoversGatewaysFromServers(t *testing.T) {
	api1 := newCapacityTargetServer(t, "127.0.0.1:5100")
	api2 := newCapacityTargetServer(t, "127.0.0.1:5101")

	target, err := resolveTarget(context.Background(), sendConfig{
		ServerAddrs: []string{api1.URL, api2.URL},
	})

	if err != nil {
		t.Fatalf("resolveTarget() error = %v", err)
	}
	if want := []string{"127.0.0.1:5100", "127.0.0.1:5101"}; !reflect.DeepEqual(target.GatewayAddrs, want) {
		t.Fatalf("GatewayAddrs = %#v, want %#v", target.GatewayAddrs, want)
	}
}

func TestResolveTargetUsesCurrentContextServers(t *testing.T) {
	api := newCapacityTargetServer(t, "127.0.0.1:5100")
	contextDir := t.TempDir()
	store := contextcmd.NewStore(contextDir)
	if err := store.Save(contextcmd.Context{Name: "dev", Servers: []string{api.URL}}); err != nil {
		t.Fatalf("save context: %v", err)
	}
	if err := store.Select("dev"); err != nil {
		t.Fatalf("select context: %v", err)
	}

	target, err := resolveTarget(context.Background(), sendConfig{ContextDir: contextDir})

	if err != nil {
		t.Fatalf("resolveTarget() error = %v", err)
	}
	if want := []string{"127.0.0.1:5100"}; !reflect.DeepEqual(target.GatewayAddrs, want) {
		t.Fatalf("GatewayAddrs = %#v, want %#v", target.GatewayAddrs, want)
	}
}

func TestResolveTargetErrorsWithoutGatewayServerOrCurrentContext(t *testing.T) {
	_, err := resolveTarget(context.Background(), sendConfig{ContextDir: t.TempDir()})

	if err == nil {
		t.Fatalf("expected missing target error")
	}
}

func newCapacityTargetServer(t *testing.T, tcpAddr string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/bench/v1/capacity-target", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"bench/v1","gateway":{"tcp_addr":"` + tcpAddr + `"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}
