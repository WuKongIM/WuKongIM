package config

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProxyProtocolListenerConfigurationAndEnvironmentReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wukongim.toml")
	writeFile(t, path, `
[node]
id = 1
data_dir = "`+filepath.Join(filepath.Dir(path), "data")+`"
[cluster]
listen_addr = "127.0.0.1:7001"
[[gateway.listeners]]
name = "tcp"
network = "tcp"
address = "127.0.0.1:5100"
transport = "gnet"
protocol = "wkproto"
proxy_protocol_trusted_cidrs = ["192.0.2.0/24", "2001:db8::/32"]
`)
	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"192.0.2.0/24", "2001:db8::/32"}
	if !reflect.DeepEqual(cfg.Gateway.Listeners[0].ProxyProtocolTrustedCIDRs, want) {
		t.Fatalf("trust=%v", cfg.Gateway.Listeners[0].ProxyProtocolTrustedCIDRs)
	}
	encoded, err := json.Marshal(cfg.Gateway.Listeners)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"proxy_protocol_trusted_cidrs"`) {
		t.Fatalf("JSON does not preserve config: %s", encoded)
	}
	for _, raw := range []string{
		`[{"name":"ws","network":"websocket","address":"127.0.0.1:5200","transport":"gnet","protocol":"wsmux","proxy_protocol_trusted_cidrs":["127.0.0.1/32"]}]`,
		`[{"name":"ws","network":"websocket","address":"127.0.0.1:5200","transport":"gnet","protocol":"wsmux"}]`,
	} {
		cfg, err := Load(Options{Args: []string{"-config", path}, Environ: append(cleanEnv(), "WK_GATEWAY_LISTENERS="+raw)})
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Gateway.Listeners) != 1 || cfg.Gateway.Listeners[0].Name != "ws" {
			t.Fatalf("environment did not replace list: %+v", cfg.Gateway.Listeners)
		}
		want := []string(nil)
		if strings.Contains(raw, "trusted_cidrs") {
			want = []string{"127.0.0.1/32"}
		}
		if !reflect.DeepEqual(cfg.Gateway.Listeners[0].ProxyProtocolTrustedCIDRs, want) {
			t.Fatal("environment inherited file trust rules")
		}
	}
	_, err = Load(Options{Args: []string{"-config", path}, Environ: append(cleanEnv(), `WK_GATEWAY_LISTENERS=[{"name":"tcp","proxy_protocol_trusted_cidrs":["invalid"]}]`)})
	if err == nil || !strings.Contains(err.Error(), "proxy_protocol_trusted_cidrs") {
		t.Fatalf("invalid CIDR error=%v", err)
	}
}
