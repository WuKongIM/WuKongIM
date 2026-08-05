package chatlifecycle

import (
	"strings"
	"testing"
)

func TestFormalObservationTopology(t *testing.T) {
	observation := DefaultConfig().Observation
	if len(observation.ServiceNodes) != 3 || len(observation.Workers) != 3 || len(observation.HostMetrics) != 3 {
		t.Fatalf("observation roles = %+v", observation)
	}
	if len(observation.APIAddrs) != 3 || len(observation.GatewayTCPAddrs) != 3 {
		t.Fatalf("API/gateway pools = %+v", observation)
	}
	for index, endpoint := range observation.HostMetrics {
		if endpoint.Mountpoint == "" || endpoint.Device == "" {
			t.Fatalf("host metrics[%d] disk selector = %+v, want explicit mountpoint/device", index, endpoint)
		}
	}
}

func TestDiskSelectorsAreRequiredOnlyForHostMetrics(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"missing mountpoint", func(c *Config) { c.Observation.HostMetrics[0].Mountpoint = "" }, "observation.host_metrics[0].mountpoint: is required"},
		{"missing device", func(c *Config) { c.Observation.HostMetrics[0].Device = "" }, "observation.host_metrics[0].device: is required"},
		{"relative mountpoint", func(c *Config) { c.Observation.HostMetrics[0].Mountpoint = "data" }, "observation.host_metrics[0].mountpoint: must be an absolute clean path"},
		{"service selector", func(c *Config) { c.Observation.ServiceNodes[0].Mountpoint = "/data" }, "observation.service_nodes[0].mountpoint: is only valid for host metrics"},
		{"worker selector", func(c *Config) { c.Observation.Workers[0].Device = "/dev/data" }, "observation.workers[0].device: is only valid for host metrics"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := LocalConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestFormalConfigAllowsDeploymentSpecificObservationAddresses(t *testing.T) {
	cfg := FormalConfig()
	cfg.Observation.ServiceNodes = []EndpointDeclaration{
		{Name: "wk-node-a", Address: "https://wk-node-a.example.test:5001"},
		{Name: "wk-node-b", Address: "https://wk-node-b.example.test:5001"},
		{Name: "wk-node-c", Address: "https://wk-node-c.example.test:5001"},
	}
	cfg.Observation.Workers = []EndpointDeclaration{
		{Name: "load-a", Address: "https://load-a.example.test:19090"},
		{Name: "load-b", Address: "https://load-b.example.test:19090"},
		{Name: "load-c", Address: "https://load-c.example.test:19090"},
	}
	cfg.Observation.HostMetrics = []EndpointDeclaration{
		{Name: "metrics-a", Address: "https://metrics-a.example.test:9100", Mountpoint: "/data", Device: "/dev/data"},
		{Name: "metrics-b", Address: "https://metrics-b.example.test:9100", Mountpoint: "/data", Device: "/dev/data"},
		{Name: "metrics-c", Address: "https://metrics-c.example.test:9100", Mountpoint: "/data", Device: "/dev/data"},
	}
	cfg.Observation.APIAddrs = []string{"https://api-a.example.test:5001", "https://api-b.example.test:5001", "https://api-c.example.test:5001"}
	cfg.Observation.GatewayTCPAddrs = []string{"gateway-a.example.test:5100", "gateway-b.example.test:5100", "gateway-c.example.test:5100"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestFormalObservationRequiresThreeIngressAddresses(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"empty API pool", func(c *Config) { c.Observation.APIAddrs = nil }, "observation.api_addrs: must not be empty"},
		{"one API address", func(c *Config) { c.Observation.APIAddrs = c.Observation.APIAddrs[:1] }, "observation.api_addrs: must equal formal default"},
		{"two gateway addresses", func(c *Config) { c.Observation.GatewayTCPAddrs = c.Observation.GatewayTCPAddrs[:2] }, "observation.gateway_tcp_addrs: must equal formal default"},
		{"four gateway addresses", func(c *Config) {
			c.Observation.GatewayTCPAddrs = append(c.Observation.GatewayTCPAddrs, "gateway-d.example.test:5100")
		}, "observation.gateway_tcp_addrs: must equal formal default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := FormalConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestObservationNormalizesCrossRoleDuplicates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"name", func(c *Config) { c.Observation.Workers[0].Name = " " + c.Observation.ServiceNodes[0].Name + " " }, "observation.workers[0].name: duplicates observation.service_nodes[0].name"},
		{"address", func(c *Config) { c.Observation.Workers[0].Address = " " + c.Observation.ServiceNodes[0].Address + " " }, "observation.workers[0].address: duplicates observation.service_nodes[0].address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := FormalConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestObservationRejectsInvalidHTTPEndpoints(t *testing.T) {
	const sentinelCredential = "sentinel-credential"
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"malformed URL", func(c *Config) { c.Observation.ServiceNodes[0].Address = "http://[2001:db8::1" }, "observation.service_nodes[0].address: must be a valid absolute HTTP URL"},
		{"relative URL", func(c *Config) { c.Observation.ServiceNodes[0].Address = "/metrics" }, "observation.service_nodes[0].address: must be a valid absolute HTTP URL"},
		{"unsupported scheme", func(c *Config) { c.Observation.Workers[0].Address = "ftp://worker.example.test" }, "observation.workers[0].address: scheme must be http or https"},
		{"userinfo", func(c *Config) {
			c.Observation.HostMetrics[0].Address = "http://" + sentinelCredential + ":password@metrics.example.test"
		}, "observation.host_metrics[0].address: must not include userinfo"},
		{"query", func(c *Config) {
			c.Observation.APIAddrs[0] = "http://api.example.test/metrics?token=" + sentinelCredential
		}, "observation.api_addrs[0]: must not include a query"},
		{"fragment", func(c *Config) { c.Observation.ServiceNodes[0].Address = "http://service.example.test/metrics#private" }, "observation.service_nodes[0].address: must not include a fragment"},
		{"malformed port", func(c *Config) { c.Observation.APIAddrs[0] = "http://api.example.test:not-a-port" }, "observation.api_addrs[0]: port must be a number in 1..65535"},
		{"empty host", func(c *Config) { c.Observation.HostMetrics[0].Address = "http://:9100/metrics" }, "observation.host_metrics[0].address: host is required"},
		{"DNS root host", func(c *Config) { c.Observation.HostMetrics[0].Address = "http://.:9100/metrics" }, "observation.host_metrics[0].address: host is required"},
		{"IPv6 terminal dot", func(c *Config) { c.Observation.HostMetrics[0].Address = "http://[::1.]:9100/metrics" }, "observation.host_metrics[0].address: must be a valid absolute HTTP URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := FormalConfig()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), sentinelCredential) {
				t.Fatalf("Validate() error leaked credential sentinel: %v", err)
			}
		})
	}
}

func TestObservationRejectsInvalidGatewayEndpoints(t *testing.T) {
	const sentinelCredential = "sentinel-credential"
	tests := []struct {
		name    string
		address string
		want    string
	}{
		{"missing port", "gateway.example.test", "must be a TCP host:port"},
		{"URL", "http://gateway.example.test:5100", "must be a TCP host:port"},
		{"userinfo", sentinelCredential + "@gateway.example.test:5100", "must not include userinfo"},
		{"path", "gateway.example.test:5100/metrics", "must not include a path, query, or fragment"},
		{"bad port", "gateway.example.test:not-a-port", "port must be a number in 1..65535"},
		{"out of range port", "gateway.example.test:65536", "port must be a number in 1..65535"},
		{"empty host", ":5100", "host is required"},
		{"DNS root host", ".:5100", "host is required"},
		{"IPv6 terminal dot", "[::1.]:5100", "host must be a valid IP address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := FormalConfig()
			cfg.Observation.GatewayTCPAddrs[0] = tt.address
			want := "observation.gateway_tcp_addrs[0]: " + tt.want
			err := cfg.Validate()
			if err == nil || err.Error() != want {
				t.Fatalf("Validate() error = %v, want %q", err, want)
			}
			if strings.Contains(err.Error(), sentinelCredential) {
				t.Fatalf("Validate() error leaked credential sentinel: %v", err)
			}
		})
	}
}

func TestObservationAcceptsRoleSpecificEndpointForms(t *testing.T) {
	cfg := FormalConfig()
	cfg.Observation.ServiceNodes = []EndpointDeclaration{
		{Name: "service-dns", Address: "https://service.example.test/base/"},
		{Name: "service-ipv4", Address: "http://192.0.2.10:5001"},
		{Name: "service-ipv6", Address: "http://[2001:db8::10]:5001/metrics"},
	}
	cfg.Observation.Workers = []EndpointDeclaration{
		{Name: "worker-dns", Address: "http://worker.example.test:19090"},
		{Name: "worker-ipv4", Address: "https://192.0.2.20/control"},
		{Name: "worker-ipv6", Address: "https://[2001:db8::20]/control/"},
	}
	cfg.Observation.HostMetrics = []EndpointDeclaration{
		{Name: "metrics-dns", Address: "http://metrics.example.test:9100/metrics", Mountpoint: "/data", Device: "/dev/data"},
		{Name: "metrics-ipv4", Address: "http://192.0.2.30:9100/metrics", Mountpoint: "/data", Device: "/dev/data"},
		{Name: "metrics-ipv6", Address: "http://[2001:db8::30]:9100/metrics", Mountpoint: "/data", Device: "/dev/data"},
	}
	cfg.Observation.APIAddrs = []string{
		"http://api.example.test:5001/base",
		"https://192.0.2.40/api/",
		"https://[2001:db8::40]:5443/api",
	}
	cfg.Observation.GatewayTCPAddrs = []string{
		" gateway.example.test:5100 ",
		"192.0.2.50:5100",
		"[2001:db8::50]:5100",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestObservationCanonicalizesHTTPDuplicateKeys(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{
			name: "same role host case default port and root slash",
			mutate: func(c *Config) {
				c.Observation.ServiceNodes[1].Address = " HTTP://SERVICE-1.INVALID:80/ "
			},
			want: "observation.service_nodes[1].address: duplicates observation.service_nodes[0].address",
		},
		{
			name: "cross role canonical IPv6 and trailing slash",
			mutate: func(c *Config) {
				c.Observation.ServiceNodes[0].Address = "http://[2001:0db8:0:0::1]:80/metrics"
				c.Observation.Workers[0].Address = "HTTP://[2001:db8::1]/metrics/"
			},
			want: "observation.workers[0].address: duplicates observation.service_nodes[0].address",
		},
		{
			name: "API clean base path and trailing slash",
			mutate: func(c *Config) {
				c.Observation.APIAddrs[0] = "http://api.example.test:80/base"
				c.Observation.APIAddrs[1] = " HTTP://API.EXAMPLE.TEST/base/./ "
			},
			want: "observation.api_addrs[1]: duplicates observation.api_addrs[0]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := FormalConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestObservationCanonicalizesTerminalDNSRootDot(t *testing.T) {
	const sentinelAuthority = "sentinel-dns-authority.example.test"
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{
			name: "same HTTP pool",
			mutate: func(c *Config) {
				c.Observation.APIAddrs[0] = "http://" + sentinelAuthority + ":5001/base"
				c.Observation.APIAddrs[1] = "HTTP://SENTINEL-DNS-AUTHORITY.EXAMPLE.TEST.:05001/base/"
			},
			want: "observation.api_addrs[1]: duplicates observation.api_addrs[0]",
		},
		{
			name: "cross-role HTTP endpoints",
			mutate: func(c *Config) {
				c.Observation.ServiceNodes[0].Address = "http://" + sentinelAuthority + ":5001/metrics"
				c.Observation.Workers[0].Address = "HTTP://SENTINEL-DNS-AUTHORITY.EXAMPLE.TEST.:05001/metrics/"
			},
			want: "observation.workers[0].address: duplicates observation.service_nodes[0].address",
		},
		{
			name: "API and gateway authority",
			mutate: func(c *Config) {
				c.Observation.APIAddrs[0] = "http://" + sentinelAuthority + ":5001/base"
				c.Observation.GatewayTCPAddrs[0] = "SENTINEL-DNS-AUTHORITY.EXAMPLE.TEST.:05001"
			},
			want: "observation.gateway_tcp_addrs[0]: aliases observation.api_addrs[0]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := FormalConfig()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
			if strings.Contains(strings.ToLower(err.Error()), sentinelAuthority) {
				t.Fatalf("Validate() error leaked authority sentinel: %v", err)
			}
		})
	}
}

func TestObservationCanonicalizesGatewayDuplicateKeys(t *testing.T) {
	cfg := FormalConfig()
	cfg.Observation.GatewayTCPAddrs[0] = "[2001:0db8:0:0::1]:5100"
	cfg.Observation.GatewayTCPAddrs[1] = " [2001:db8::1]:05100 "
	want := "observation.gateway_tcp_addrs[1]: duplicates observation.gateway_tcp_addrs[0]"
	if err := cfg.Validate(); err == nil || err.Error() != want {
		t.Fatalf("Validate() error = %v, want %q", err, want)
	}
}

func TestObservationComparesAPIGatewayCanonicalAuthority(t *testing.T) {
	t.Run("same authority aliases", func(t *testing.T) {
		cfg := FormalConfig()
		cfg.Observation.APIAddrs[0] = "http://api.example.test:5001/base/"
		cfg.Observation.GatewayTCPAddrs[0] = " API.EXAMPLE.TEST:05001 "
		want := "observation.gateway_tcp_addrs[0]: aliases observation.api_addrs[0]"
		if err := cfg.Validate(); err == nil || err.Error() != want {
			t.Fatalf("Validate() error = %v, want %q", err, want)
		}
	})

	t.Run("same host different ports", func(t *testing.T) {
		cfg := FormalConfig()
		cfg.Observation.APIAddrs = []string{
			"http://api.example.test:5001",
			"http://api.example.test:5002",
			"http://api.example.test:5003",
		}
		cfg.Observation.GatewayTCPAddrs = []string{
			"API.EXAMPLE.TEST:5100",
			"api.example.test:5101",
			"api.example.test:5102",
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})
}

func TestLocalObservationRequiresThreeRoleDeclarations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"service nodes", func(c *Config) { c.Observation.ServiceNodes = c.Observation.ServiceNodes[:2] }, "observation.service_nodes: must contain exactly 3 entries for local baseline"},
		{"workers", func(c *Config) { c.Observation.Workers = c.Observation.Workers[:2] }, "observation.workers: must contain exactly 3 entries for local baseline"},
		{"host metrics", func(c *Config) { c.Observation.HostMetrics = c.Observation.HostMetrics[:2] }, "observation.host_metrics: must contain exactly 3 entries for local baseline"},
		{"API pool", func(c *Config) { c.Observation.APIAddrs = c.Observation.APIAddrs[:2] }, "observation.api_addrs: must contain exactly 3 entries for local baseline"},
		{"gateway pool", func(c *Config) { c.Observation.GatewayTCPAddrs = c.Observation.GatewayTCPAddrs[:2] }, "observation.gateway_tcp_addrs: must contain exactly 3 entries for local baseline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := LocalConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLocalConfigAllowsReplacementObservationAddresses(t *testing.T) {
	cfg := LocalConfig()
	cfg.Observation.ServiceNodes[0].Address = "http://127.0.0.1:25001"
	cfg.Observation.Workers[0].Address = "http://127.0.0.1:29091"
	cfg.Observation.HostMetrics[0].Address = "http://127.0.0.1:29101"
	cfg.Observation.APIAddrs[0] = "http://127.0.0.1:25011"
	cfg.Observation.GatewayTCPAddrs[0] = "127.0.0.1:25101"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestObservationRejectsUnusableFormalRoles(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"empty service declarations", func(c *Config) { c.Observation.ServiceNodes = nil }, "observation.service_nodes: must not be empty"},
		{"wrong service count", func(c *Config) { c.Observation.ServiceNodes = c.Observation.ServiceNodes[:2] }, "observation.service_nodes: must equal formal default"},
		{"blank address", func(c *Config) { c.Observation.Workers[0].Address = " " }, "observation.workers[0].address: is required"},
		{"duplicate API", func(c *Config) { c.Observation.APIAddrs[1] = c.Observation.APIAddrs[0] }, "observation.api_addrs[1]: duplicates observation.api_addrs[0]"},
		{"API gateway alias", func(c *Config) { c.Observation.GatewayTCPAddrs[0] = "api-1.invalid:80" }, "observation.gateway_tcp_addrs[0]: aliases observation.api_addrs[0]"},
		{"cross-role declaration", func(c *Config) { c.Observation.Workers[0].Address = c.Observation.ServiceNodes[0].Address }, "observation.workers[0].address: duplicates observation.service_nodes[0].address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}
