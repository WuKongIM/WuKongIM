package chatlifecycle

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Profile != ProfileFormal {
		t.Fatalf("profile = %q, want %q", cfg.Profile, ProfileFormal)
	}
	if cfg.RunID == "" || cfg.Seed == 0 {
		t.Fatalf("identity = run_id %q, seed %d; want non-empty run ID and nonzero seed", cfg.RunID, cfg.Seed)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConfigUsesTopLevelIdentityAndMode(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.RunID == "" || cfg.Seed == 0 {
		t.Fatalf("top-level identity = run_id %q, seed %d", cfg.RunID, cfg.Seed)
	}
	if cfg.Profile != ProfileFormal || cfg.Mode != ModeSoak {
		t.Fatalf("top-level profile/mode = %q/%q", cfg.Profile, cfg.Mode)
	}
}

func TestLifecycleDistributionUsesNamedClasses(t *testing.T) {
	cfg := DefaultConfig()
	lifecycle := cfg.Workload.Lifecycle
	if lifecycle.OneShot.Percent != 60 || lifecycle.Revisit.Percent != 25 {
		t.Fatalf("one-shot/revisit = %+v/%+v", lifecycle.OneShot, lifecycle.Revisit)
	}
	if lifecycle.Rotating != (LifecycleBucket{Percent: 10, ActiveDuration: DurationRange{Min: 20 * time.Minute, Max: 40 * time.Minute}}) {
		t.Fatalf("rotating = %+v", lifecycle.Rotating)
	}
	if lifecycle.Long != (LifecycleBucket{Percent: 5, ActiveDuration: DurationRange{Min: 2 * time.Hour, Max: 4 * time.Hour}}) {
		t.Fatalf("long = %+v", lifecycle.Long)
	}
}

func TestLifecycleClassesRejectMissingAndWrongClassShapes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"missing class share", func(c *Config) { c.Profile = ProfileLocal; c.Workload.Lifecycle.Revisit.Percent = 0 }, "workload.lifecycle: percentages must total 100"},
		{"one-shot duration", func(c *Config) {
			c.Profile = ProfileLocal
			c.Workload.Lifecycle.OneShot.ActiveDuration = DurationRange{Min: time.Minute, Max: 2 * time.Minute}
		}, "workload.lifecycle.one_shot.active_duration: must be empty"},
		{"rotating missing duration", func(c *Config) {
			c.Profile = ProfileLocal
			c.Workload.Lifecycle.Rotating.ActiveDuration = DurationRange{}
		}, "workload.lifecycle.rotating.active_duration.min: must be greater than zero"},
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

func TestFormalObservationTopology(t *testing.T) {
	observation := DefaultConfig().Observation
	if len(observation.ServiceNodes) != 3 || len(observation.Workers) != 3 || len(observation.HostMetrics) != 3 {
		t.Fatalf("observation roles = %+v", observation)
	}
	if len(observation.APIAddrs) != 3 || len(observation.GatewayTCPAddrs) != 3 {
		t.Fatalf("API/gateway pools = %+v", observation)
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
		{Name: "metrics-a", Address: "https://metrics-a.example.test:9100"},
		{Name: "metrics-b", Address: "https://metrics-b.example.test:9100"},
		{Name: "metrics-c", Address: "https://metrics-c.example.test:9100"},
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
		{Name: "metrics-dns", Address: "http://metrics.example.test:9100/metrics"},
		{Name: "metrics-ipv4", Address: "http://192.0.2.30:9100/metrics"},
		{Name: "metrics-ipv6", Address: "http://[2001:db8::30]:9100/metrics"},
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

func TestFormalConfigRejectsApprovedDefaultMutations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		path   string
	}{
		{"workers", func(c *Config) { c.Workload.Workers = 4 }, "workload.workers"},
		{"online users", func(c *Config) { c.Workload.OnlineUsers = 10_001 }, "workload.online_users"},
		{"new users", func(c *Config) { c.Workload.NewUsersPerDay = 250_001 }, "workload.new_users_per_day"},
		{"send rate", func(c *Config) { c.Workload.SendRatePerSecond = 2_001; c.Workload.MaxGlobalBurst = 4_002 }, "workload.send_rate_per_second"},
		{"traffic person", func(c *Config) { c.Workload.Traffic.PersonPercent = 89; c.Workload.Traffic.GroupPercent = 11 }, "workload.traffic.person_percent"},
		{"hot person", func(c *Config) { c.Workload.HotSet.PersonChannels = 8_001 }, "workload.hot_set.person_channels"},
		{"sample cadence", func(c *Config) { c.Workload.RuntimeSampling.Every = 11 * time.Minute }, "workload.runtime_sampling.every"},
		{"sample size", func(c *Config) { c.Workload.RuntimeSampling.Size = 1_199 }, "workload.runtime_sampling.size"},
		{"max channels", func(c *Config) { c.Workload.MaxChannelsPerNode = 50_001 }, "workload.max_channels_per_node"},
		{"login", func(c *Config) { c.Workload.Login.NewPercent = 81; c.Workload.Login.ReturningPercent = 19 }, "workload.login.new_percent"},
		{"session", func(c *Config) { c.Workload.Sessions[0].Min = 6 * time.Minute }, "workload.sessions[0].min"},
		{"one-shot", func(c *Config) { c.Workload.Lifecycle.OneShot.Percent = 59; c.Workload.Lifecycle.Revisit.Percent = 26 }, "workload.lifecycle.one_shot.percent"},
		{"rotating", func(c *Config) { c.Workload.Lifecycle.Rotating.ActiveDuration.Min = 21 * time.Minute }, "workload.lifecycle.rotating.active_duration.min"},
		{"payload", func(c *Config) { c.Workload.Payloads[0].Bytes = 257 }, "workload.payloads[0].bytes"},
		{"direction", func(c *Config) {
			c.Workload.PersonDirection.AlternatingPercent = 69
			c.Workload.PersonDirection.OneWayPercent = 31
		}, "workload.person_direction.alternating_percent"},
		{"relationship", func(c *Config) { c.Workload.Relationship.ReturningMessages.Max = 4 }, "workload.relationship.returning_messages.max"},
		{"retry count", func(c *Config) { c.Workload.Retry.MaxCount = 2 }, "workload.retry.max_count"},
		{"retry bases", func(c *Config) {
			c.Workload.Retry.Delays[0], c.Workload.Retry.Delays[1] = c.Workload.Retry.Delays[1], c.Workload.Retry.Delays[0]
		}, "workload.retry.delays[0]"},
		{"groups", func(c *Config) { c.Workload.Groups.Small++; c.Workload.Groups.Medium-- }, "workload.groups.small"},
		{"group cadence", func(c *Config) { c.Workload.Groups.VeryLargeSendEvery = 2 * time.Minute }, "workload.groups.very_large_send_every"},
		{"filesystem", func(c *Config) { c.Thresholds.MinimumDataFilesystemBytes++ }, "thresholds.minimum_data_filesystem_bytes"},
		{"disk free", func(c *Config) { c.Thresholds.DiskSafeStopFreePercent = 6 }, "thresholds.disk_safe_stop_free_percent"},
		{"terminal sends", func(c *Config) { c.Thresholds.Correctness.TerminalSends = 1 }, "thresholds.correctness.terminal_sends"},
		{"overall rational", func(c *Config) { c.Thresholds.Correctness.OverallFirstAttemptFailure.MaxFailures = 2 }, "thresholds.correctness.overall_first_attempt_failure.max_failures"},
		{"hot latency", func(c *Config) { c.Thresholds.Latency.HotSendACK.P99 = 201 * time.Millisecond }, "thresholds.latency.hot_sendack.p99"},
		{"resource", func(c *Config) { c.Thresholds.Resource.GoroutineGrowthPercent = 6 }, "thresholds.resource.goroutine_growth_percent"},
		{"health", func(c *Config) { c.Thresholds.Cluster.HealthPollEvery = 6 * time.Second }, "thresholds.cluster.health_poll_every"},
		{"warmup", func(c *Config) { c.Thresholds.Timeline.Warmup = 3 * time.Hour }, "thresholds.timeline.warmup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := FormalConfig()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.HasPrefix(err.Error(), tt.path+": must equal formal default") {
				t.Fatalf("Validate() error = %v, want formal default error at %s", err, tt.path)
			}
		})
	}
}

func TestLocalProfilePreservesTopologyAndRealSync(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"topology", func(c *Config) { c.Workload.Topology.HashSlots = 255 }, "workload.topology"},
		{"sync version", func(c *Config) { c.Workload.Sync.Version = 1 }, "workload.sync.version"},
		{"sync limit", func(c *Config) { c.Workload.Sync.Limit = 499 }, "workload.sync.limit"},
		{"sync message count", func(c *Config) { c.Workload.Sync.MessageCount = 19 }, "workload.sync.message_count"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Profile = ProfileLocal
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.HasPrefix(err.Error(), tt.want+":") {
				t.Fatalf("Validate() error = %v, want %s field path", err, tt.want)
			}
		})
	}
}

func TestGenericThresholdValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"negative correctness", func(c *Config) { c.Thresholds.Correctness.Losses = -1 }, "thresholds.correctness.losses: must not be negative"},
		{"zero rational denominator", func(c *Config) { c.Thresholds.Correctness.OverallFirstAttemptFailure.PerAttempts = 0 }, "thresholds.correctness.overall_first_attempt_failure.per_attempts: must be greater than zero"},
		{"rational numerator exceeds denominator", func(c *Config) { c.Thresholds.Correctness.OverallFirstAttemptFailure.MaxFailures = 10_001 }, "thresholds.correctness.overall_first_attempt_failure.max_failures: must not exceed per_attempts"},
		{"invalid rational operator", func(c *Config) { c.Thresholds.Correctness.OverallFirstAttemptFailure.Operator = Comparison("=") }, "thresholds.correctness.overall_first_attempt_failure.operator: must be < or <="},
		{"warmup ordering", func(c *Config) { c.Thresholds.Timeline.Warmup = c.Thresholds.Timeline.Checkpoint }, "thresholds.timeline.warmup: must be before checkpoint"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Profile = ProfileLocal
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLocalProfileAllowsShorterShakeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Profile = ProfileLocal
	cfg.Workload.OnlineUsers = 10
	cfg.Workload.NewUsersPerDay = 100
	cfg.Workload.SendRatePerSecond = 10
	cfg.Workload.MaxGlobalBurst = 20
	cfg.Workload.Sessions[0] = DurationShare{Percent: 25, Min: time.Minute, Max: 2 * time.Minute}
	cfg.Thresholds.Timeline = TimelineThresholds{Warmup: time.Minute, Checkpoint: 2 * time.Minute, Final: 3 * time.Minute}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestCapacityModeRequiresFormalEvidenceAndExactStaircase(t *testing.T) {
	validCheckpoint := AgedCheckpoint{Reference: "reports/formal-72h", Completed: true, Passed: true, Duration: 72 * time.Hour}
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"local profile", func(c *Config) { c.Profile = ProfileLocal }, "profile: must be formal in capacity mode"},
		{"start rate", func(c *Config) { c.Capacity.StartRatePerSecond = 2_001 }, "capacity.start_rate_per_second: must equal formal default"},
		{"recovery rate", func(c *Config) { c.Capacity.RecoveryRatePerSecond = 2_001 }, "capacity.recovery_rate_per_second: must equal formal default"},
		{"step percent", func(c *Config) { c.Capacity.StepPercent = 26 }, "capacity.step_percent: must equal formal default"},
		{"recovery", func(c *Config) { c.Capacity.RecoveryDuration = 31 * time.Minute }, "capacity.recovery_duration: must equal formal default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Mode = ModeCapacity
			cfg.Capacity.AgedCheckpoint = validCheckpoint
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestFormalSoakRequiresExactCapacityLeaves(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"start rate", func(c *Config) { c.Capacity.StartRatePerSecond = 2_001 }, "capacity.start_rate_per_second: must equal formal default"},
		{"recovery rate", func(c *Config) { c.Capacity.RecoveryRatePerSecond = 2_001 }, "capacity.recovery_rate_per_second: must equal formal default"},
		{"step percent", func(c *Config) { c.Capacity.StepPercent = 26 }, "capacity.step_percent: must equal formal default"},
		{"refine percent", func(c *Config) { c.Capacity.RefinePercent = 11 }, "capacity.refine_percent: must equal formal default"},
		{"stabilize", func(c *Config) { c.Capacity.Step.Stabilize = 11 * time.Minute }, "capacity.step.stabilize: must equal formal default"},
		{"measure", func(c *Config) { c.Capacity.Step.Measure = 21 * time.Minute }, "capacity.step.measure: must equal formal default"},
		{"recovery duration", func(c *Config) { c.Capacity.RecoveryDuration = 31 * time.Minute }, "capacity.recovery_duration: must equal formal default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := FormalConfig()
			if cfg.Mode != ModeSoak {
				t.Fatalf("Mode = %q, want %q", cfg.Mode, ModeSoak)
			}
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestFailureRatioStrictZeroBoundary(t *testing.T) {
	tests := []struct {
		name    string
		limit   FailureRateLimit
		wantErr string
	}{
		{
			name:    "strict zero is unsatisfiable",
			limit:   FailureRateLimit{MaxFailures: 0, PerAttempts: 1_000, Operator: ComparisonLessThan},
			wantErr: "thresholds.correctness.overall_first_attempt_failure.max_failures: must be greater than zero when operator is <",
		},
		{
			name:  "inclusive zero is zero tolerance",
			limit: FailureRateLimit{MaxFailures: 0, PerAttempts: 1_000, Operator: ComparisonLessOrEqual},
		},
		{
			name:  "strict one permits zero failures",
			limit: FailureRateLimit{MaxFailures: 1, PerAttempts: 1_000, Operator: ComparisonLessThan},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := FormalConfig()
			cfg.Profile = ProfileLocal
			cfg.Thresholds.Correctness.OverallFirstAttemptFailure = tt.limit
			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("Validate() error = %v, want %q", err, tt.wantErr)
			}
		})
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

func TestFormalConfigDefaults(t *testing.T) {
	cfg := FormalConfig()

	if cfg.Workload.Workers != 3 || cfg.Workload.OnlineUsers != 10_000 || cfg.Workload.NewUsersPerDay != 250_000 || cfg.Workload.SendRatePerSecond != 2_000 {
		t.Fatalf("core workload = %+v", cfg.Workload)
	}
	if cfg.Workload.Traffic.PersonPercent != 90 || cfg.Workload.Traffic.GroupPercent != 10 || cfg.Workload.HotSet.PersonChannels != 8_000 || cfg.Workload.HotSet.GroupChannels != 2_000 {
		t.Fatalf("traffic/hot set = %+v / %+v", cfg.Workload.Traffic, cfg.Workload.HotSet)
	}
	if cfg.Workload.Topology != (TopologyConfig{LogicalSlotGroups: 12, HashSlots: 256, SlotReplicas: 3, ChannelReplicas: 3}) {
		t.Fatalf("topology = %+v", cfg.Workload.Topology)
	}
	if cfg.Workload.RuntimeSampling.Every != 10*time.Minute || cfg.Workload.RuntimeSampling.Size != 1_200 {
		t.Fatalf("runtime sampling = %+v", cfg.Workload.RuntimeSampling)
	}
	if cfg.Workload.Sync != (SyncConfig{Version: 0, Limit: 500, MessageCount: 20}) {
		t.Fatalf("sync = %+v", cfg.Workload.Sync)
	}
	if cfg.Workload.BurstCredit != 2*time.Second || cfg.Workload.MaxGlobalBurst != 4_000 || cfg.Workload.MaxChannelsPerNode != 50_000 {
		t.Fatalf("burst/channel limits = %+v", cfg.Workload)
	}

	assertPercentPair(t, "login", cfg.Workload.Login.NewPercent, cfg.Workload.Login.ReturningPercent, 80, 20)
	assertDurationBuckets(t, "session", cfg.Workload.Sessions, []DurationShare{
		{Percent: 25, Min: 5 * time.Minute, Max: 15 * time.Minute},
		{Percent: 50, Min: 15 * time.Minute, Max: 45 * time.Minute},
		{Percent: 20, Min: 45 * time.Minute, Max: 120 * time.Minute},
		{Percent: 5, Min: 2 * time.Hour, Max: 6 * time.Hour},
	})
	if cfg.Workload.Lifecycle != (LifecycleDistribution{OneShot: LifecycleBucket{Percent: 60}, Revisit: LifecycleBucket{Percent: 25}, Rotating: LifecycleBucket{Percent: 10, ActiveDuration: DurationRange{Min: 20 * time.Minute, Max: 40 * time.Minute}}, Long: LifecycleBucket{Percent: 5, ActiveDuration: DurationRange{Min: 2 * time.Hour, Max: 4 * time.Hour}}}) {
		t.Fatalf("lifecycle = %+v", cfg.Workload.Lifecycle)
	}
	if got, want := cfg.Workload.Payloads, []PayloadShare{{Percent: 70, Bytes: 256}, {Percent: 25, Bytes: 1_024}, {Percent: 4, Bytes: 4_096}, {Percent: 1, Bytes: 16_384}}; !samePayloads(got, want) {
		t.Fatalf("payloads = %+v, want %+v", got, want)
	}
	assertPercentPair(t, "person direction", cfg.Workload.PersonDirection.AlternatingPercent, cfg.Workload.PersonDirection.OneWayPercent, 70, 30)
	if cfg.Workload.Relationship.InitialMessages != (IntRange{Min: 2, Max: 8}) || cfg.Workload.Relationship.InitialMessageWindow != (DurationRange{Min: 5 * time.Second, Max: 30 * time.Second}) || cfg.Workload.Relationship.ReturningMessages != (IntRange{Min: 2, Max: 5}) {
		t.Fatalf("relationship = %+v", cfg.Workload.Relationship)
	}
	assertPercentPair(t, "returning age", cfg.Workload.Relationship.ReturningLast24hPercent, cfg.Workload.Relationship.ReturningOlderPercent, 80, 20)
	if cfg.Workload.Retry.MaxCount != 3 || len(cfg.Workload.Retry.Delays) != 3 || cfg.Workload.Retry.Delays[0] != 100*time.Millisecond || cfg.Workload.Retry.Delays[1] != 500*time.Millisecond || cfg.Workload.Retry.Delays[2] != 2*time.Second {
		t.Fatalf("retry = %+v", cfg.Workload.Retry)
	}
	if cfg.Workload.Groups != (GroupCatalogConfig{Small: 1_600, Medium: 300, Large: 99, VeryLarge: 1, VeryLargeMembers: 100_000, FixedMembership: true, VeryLargeSendEvery: time.Minute}) {
		t.Fatalf("groups = %+v", cfg.Workload.Groups)
	}

	if cfg.Thresholds.MinimumDataFilesystemBytes != 1_000_000_000_000 || cfg.Thresholds.DiskSafeStopFreePercent != 5 {
		t.Fatalf("disk thresholds = %+v", cfg.Thresholds)
	}
	if cfg.Thresholds.Cluster.HealthPollEvery != 5*time.Second || cfg.Thresholds.Cluster.UnhealthyFailAfter != 30*time.Second || cfg.Thresholds.Cluster.LeaderImbalancePercent != 20 || cfg.Thresholds.Cluster.LeaderImbalanceFor != 10*time.Minute {
		t.Fatalf("cluster thresholds = %+v", cfg.Thresholds.Cluster)
	}
	if cfg.Thresholds.Timeline.Warmup != 2*time.Hour || cfg.Thresholds.Timeline.Checkpoint != 24*time.Hour || cfg.Thresholds.Timeline.Final != 72*time.Hour {
		t.Fatalf("timeline = %+v", cfg.Thresholds.Timeline)
	}
	if cfg.Thresholds.Correctness.TerminalSends != 0 || cfg.Thresholds.Correctness.ActivationRejections != 0 || cfg.Thresholds.Correctness.Losses != 0 || cfg.Thresholds.Correctness.Duplicates != 0 || cfg.Thresholds.Correctness.Corruptions != 0 || cfg.Thresholds.Correctness.SequenceRegressions != 0 {
		t.Fatalf("correctness = %+v", cfg.Thresholds.Correctness)
	}
	if cfg.Thresholds.Correctness.OverallFirstAttemptFailure != (FailureRateLimit{MaxFailures: 1, PerAttempts: 10_000, Operator: ComparisonLessThan}) || cfg.Thresholds.Correctness.AnyMinuteFirstAttemptFailure != (FailureRateLimit{MaxFailures: 1, PerAttempts: 1_000, Operator: ComparisonLessOrEqual}) {
		t.Fatalf("failure limits = %+v", cfg.Thresholds.Correctness)
	}
	if cfg.Thresholds.Latency.HotSendACK != (LatencyLimit{P99: 200 * time.Millisecond, P999: time.Second}) || cfg.Thresholds.Latency.Cold != (LatencyLimit{P99: 2 * time.Second, P999: 5 * time.Second}) || cfg.Thresholds.Latency.Sync != (LatencyLimit{P99: time.Second, P999: 3 * time.Second}) || cfg.Thresholds.Latency.SingleAnomaly != 10*time.Second || cfg.Thresholds.Latency.SustainedBreachWindow != 5*time.Minute {
		t.Fatalf("latency = %+v", cfg.Thresholds.Latency)
	}
	if cfg.Thresholds.Resource.ForcedGCLiveHeapGrowthPercent != 5 || cfg.Thresholds.Resource.ForcedGCLiveHeapWindow != 6*time.Hour || cfg.Thresholds.Resource.GoroutineGrowthPercent != 5 || cfg.Thresholds.Resource.GoroutineGrowthWindow != 24*time.Hour {
		t.Fatalf("resource = %+v", cfg.Thresholds.Resource)
	}
	if cfg.Capacity.StartRatePerSecond != 2_000 || cfg.Capacity.RecoveryRatePerSecond != 2_000 || cfg.Capacity.StepPercent != 25 || cfg.Capacity.RefinePercent != 10 || cfg.Capacity.Step.Stabilize != 10*time.Minute || cfg.Capacity.Step.Measure != 20*time.Minute || cfg.Capacity.RecoveryDuration != 30*time.Minute {
		t.Fatalf("capacity = %+v", cfg.Capacity)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"blank run ID", func(c *Config) { c.RunID = " " }, "run_id: is required"},
		{"zero seed", func(c *Config) { c.Seed = 0 }, "seed: must be nonzero"},
		{"formal workers", func(c *Config) { c.Workload.Workers = 2 }, "workload.workers: must equal formal default"},
		{"nonpositive users", func(c *Config) { c.Workload.OnlineUsers = 0 }, "workload.online_users: must be greater than zero"},
		{"formal topology", func(c *Config) { c.Workload.Topology.HashSlots = 255 }, "workload.topology: must preserve 12 logical slot groups, 256 hash slots, and 3 replicas"},
		{"sample size", func(c *Config) { c.Workload.RuntimeSampling.Size = 1_201 }, "workload.runtime_sampling.size: must not exceed 1200"},
		{"sample cadence", func(c *Config) { c.Workload.RuntimeSampling.Every = 0 }, "workload.runtime_sampling.every: must be greater than zero"},
		{"login share", func(c *Config) { c.Workload.Login.NewPercent = 79 }, "workload.login: percentages must total 100"},
		{"session range", func(c *Config) { c.Workload.Sessions[0].Min = 0 }, "workload.sessions[0].min: must be greater than zero"},
		{"lifecycle share", func(c *Config) { c.Workload.Lifecycle.OneShot.Percent = 59 }, "workload.lifecycle: percentages must total 100"},
		{"payload share", func(c *Config) { c.Workload.Payloads[0].Percent = 69 }, "workload.payloads: percentages must total 100"},
		{"person direction", func(c *Config) { c.Workload.PersonDirection.OneWayPercent = 31 }, "workload.person_direction: percentages must total 100"},
		{"relationship messages", func(c *Config) { c.Workload.Relationship.InitialMessages.Min = 9 }, "workload.relationship.initial_messages: min must not exceed max"},
		{"retry count", func(c *Config) { c.Workload.Retry.MaxCount = 4 }, "workload.retry.max_count: must not exceed 3"},
		{"retry delays", func(c *Config) { c.Workload.Retry.Delays = c.Workload.Retry.Delays[:2] }, "workload.retry.delays: must contain exactly 3 delays"},
		{"formal sync", func(c *Config) { c.Workload.Sync.Limit = 499 }, "workload.sync.limit: must equal 500 for real sync"},
		{"burst cap", func(c *Config) { c.Workload.MaxGlobalBurst = 3_999 }, "workload.max_global_burst: must equal burst_credit times send_rate_per_second"},
		{"filesystem", func(c *Config) { c.Thresholds.MinimumDataFilesystemBytes = 999_999_999_999 }, "thresholds.minimum_data_filesystem_bytes: must equal formal default"},
		{"disk percent", func(c *Config) { c.Thresholds.DiskSafeStopFreePercent = 0 }, "thresholds.disk_safe_stop_free_percent: must be in 1..100"},
		{"timeline", func(c *Config) { c.Thresholds.Timeline.Checkpoint = c.Thresholds.Timeline.Final }, "thresholds.timeline.checkpoint: must be before final"},
		{"formal timeline", func(c *Config) { c.Thresholds.Timeline.Warmup = time.Hour }, "thresholds.timeline.warmup: must equal formal default"},
		{"groups", func(c *Config) { c.Workload.Groups.Small-- }, "workload.groups: catalog counts must total 2000"},
		{"very large group", func(c *Config) { c.Workload.Groups.VeryLarge = 0; c.Workload.Groups.Small++ }, "workload.groups.very_large: must equal 1"},
		{"fixed membership", func(c *Config) { c.Workload.Groups.FixedMembership = false }, "workload.groups.fixed_membership: must be true"},
		{"blank endpoint", func(c *Config) { c.Observation.ServiceNodes[0].Name = " " }, "observation.service_nodes[0].name: is required"},
		{"duplicate endpoint", func(c *Config) { c.Observation.ServiceNodes[1].Name = c.Observation.ServiceNodes[0].Name }, "observation.service_nodes[1].name: duplicates observation.service_nodes[0].name"},
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

func TestConfigValidateCapacityAgedCheckpoint(t *testing.T) {
	validCheckpoint := AgedCheckpoint{Reference: "reports/formal-72h", Completed: true, Passed: true, Duration: 72 * time.Hour}
	tests := []struct {
		name       string
		checkpoint AgedCheckpoint
		want       string
	}{
		{"missing", AgedCheckpoint{}, "capacity.aged_checkpoint.reference: is required in capacity profile"},
		{"incomplete", AgedCheckpoint{Reference: "checkpoint", Passed: true, Duration: 72 * time.Hour}, "capacity.aged_checkpoint.completed: must be true in capacity profile"},
		{"failed", AgedCheckpoint{Reference: "checkpoint", Completed: true, Duration: 72 * time.Hour}, "capacity.aged_checkpoint.passed: must be true in capacity profile"},
		{"too short", AgedCheckpoint{Reference: "checkpoint", Completed: true, Passed: true, Duration: 71*time.Hour + 59*time.Minute}, "capacity.aged_checkpoint.duration: must be at least 72h0m0s in capacity profile"},
		{"valid", validCheckpoint, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Mode = ModeCapacity
			cfg.Capacity.AgedCheckpoint = tt.checkpoint
			err := cfg.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestConfigValidateCapacityStaircase(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"zero step", func(c *Config) { c.Capacity.StepPercent = 0 }, "capacity.step_percent: must be in 1..100"},
		{"zero stabilize", func(c *Config) { c.Capacity.Step.Stabilize = 0 }, "capacity.step.stabilize: must be greater than zero"},
		{"wrong step total", func(c *Config) { c.Capacity.Step.Measure = 19 * time.Minute }, "capacity.step: stabilize plus measure must equal 30m0s"},
		{"zero recovery", func(c *Config) { c.Capacity.RecoveryDuration = 0 }, "capacity.recovery_duration: must be greater than zero"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Mode = ModeCapacity
			cfg.Capacity.AgedCheckpoint = AgedCheckpoint{Reference: "reports/formal-72h", Completed: true, Passed: true, Duration: 72 * time.Hour}
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func assertPercentPair(t *testing.T, name string, first, second, wantFirst, wantSecond int) {
	t.Helper()
	if first != wantFirst || second != wantSecond {
		t.Fatalf("%s = %d/%d, want %d/%d", name, first, second, wantFirst, wantSecond)
	}
}

func assertDurationBuckets(t *testing.T, name string, got, want []DurationShare) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s buckets = %+v, want %+v", name, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s buckets[%d] = %+v, want %+v", name, index, got[index], want[index])
		}
	}
}

func samePayloads(got, want []PayloadShare) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestValidationErrorsAreFieldPaths(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Profile = Profile("unknown")
	err := cfg.Validate()
	if err == nil || !strings.HasPrefix(err.Error(), "profile:") {
		t.Fatalf("Validate() error = %v, want profile field path", err)
	}
}
