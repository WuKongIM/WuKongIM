package chatlifecycle

import (
	"fmt"
	"net"
	"net/url"
	pathpkg "path"
	"strconv"
	"strings"
	"time"
)

const (
	formalWorkers            = 3
	formalLogicalSlotGroups  = 12
	formalHashSlots          = 256
	formalReplicas           = 3
	formalRuntimeSampleSize  = 1_200
	formalSyncLimit          = 500
	formalSyncMessageCount   = 20
	formalFilesystemBytes    = int64(1_000_000_000_000)
	formalGroupCatalogTotal  = 2_000
	formalVeryLargeMembers   = 100_000
	capacityStepDuration     = 30 * time.Minute
	formalCheckpointDuration = 72 * time.Hour
)

// DefaultConfig returns the validated formal configuration for lifecycle planning.
func DefaultConfig() Config {
	return FormalConfig()
}

// LocalConfig returns a three-worker, three-node shakeout baseline. It keeps
// the formal cluster topology and real-sync shape while reducing the workload
// to 100 online users, 100 SENDs/s, 100 active channels, and a 30-minute run.
func LocalConfig() Config {
	cfg := FormalConfig()
	cfg.RunID = "local-chat-lifecycle"
	cfg.Profile = ProfileLocal
	cfg.Workload.Workers = formalWorkers
	cfg.Workload.OnlineUsers = 100
	cfg.Workload.NewUsersPerDay = 1_000
	cfg.Workload.SendRatePerSecond = 100
	cfg.Workload.HotSet = HotSetConfig{PersonChannels: 80, GroupChannels: 20}
	cfg.Workload.RuntimeSampling = RuntimeSamplingConfig{Every: time.Minute, Size: 12}
	cfg.Workload.BurstCredit = 2 * time.Second
	cfg.Workload.MaxGlobalBurst = 200
	cfg.Workload.MaxChannelsPerNode = 500
	cfg.Workload.Groups = GroupCatalogConfig{
		Small: 16, Medium: 3, VeryLarge: 1, VeryLargeMembers: 1_000,
		FixedMembership: true, VeryLargeSendEvery: time.Minute,
	}
	cfg.Observation = ObservationConfig{
		ServiceNodes:    []EndpointDeclaration{{Name: "local-service-1", Address: "http://127.0.0.1:15001"}, {Name: "local-service-2", Address: "http://127.0.0.1:15002"}, {Name: "local-service-3", Address: "http://127.0.0.1:15003"}},
		Workers:         []EndpointDeclaration{{Name: "local-worker-1", Address: "http://127.0.0.1:19091"}, {Name: "local-worker-2", Address: "http://127.0.0.1:19092"}, {Name: "local-worker-3", Address: "http://127.0.0.1:19093"}},
		HostMetrics:     []EndpointDeclaration{{Name: "local-host-metrics-1", Address: "http://127.0.0.1:19101"}, {Name: "local-host-metrics-2", Address: "http://127.0.0.1:19102"}, {Name: "local-host-metrics-3", Address: "http://127.0.0.1:19103"}},
		APIAddrs:        []string{"http://127.0.0.1:15011", "http://127.0.0.1:15012", "http://127.0.0.1:15013"},
		GatewayTCPAddrs: []string{"127.0.0.1:15101", "127.0.0.1:15102", "127.0.0.1:15103"},
		Cadence:         2 * time.Second,
	}
	cfg.Thresholds.MinimumDataFilesystemBytes = 10_000_000_000
	cfg.Thresholds.Timeline = TimelineThresholds{Warmup: 10 * time.Minute, Checkpoint: 20 * time.Minute, Final: 30 * time.Minute}
	return cfg
}

// FormalConfig returns all reviewed formal workload, threshold, and staircase defaults.
func FormalConfig() Config {
	return Config{
		RunID:   "formal-chat-lifecycle",
		Seed:    1,
		Profile: ProfileFormal,
		Mode:    ModeSoak,
		Workload: WorkloadConfig{
			Workers:            formalWorkers,
			OnlineUsers:        10_000,
			NewUsersPerDay:     250_000,
			SendRatePerSecond:  2_000,
			Traffic:            TrafficShareConfig{PersonPercent: 90, GroupPercent: 10},
			HotSet:             HotSetConfig{PersonChannels: 8_000, GroupChannels: 2_000},
			Topology:           TopologyConfig{LogicalSlotGroups: formalLogicalSlotGroups, HashSlots: formalHashSlots, SlotReplicas: formalReplicas, ChannelReplicas: formalReplicas},
			RuntimeSampling:    RuntimeSamplingConfig{Every: 10 * time.Minute, Size: formalRuntimeSampleSize},
			Sync:               SyncConfig{Version: 0, Limit: formalSyncLimit, MessageCount: formalSyncMessageCount},
			BurstCredit:        2 * time.Second,
			MaxGlobalBurst:     4_000,
			MaxChannelsPerNode: 50_000,
			Login:              LoginDistribution{NewPercent: 80, ReturningPercent: 20},
			Sessions: []DurationShare{
				{Percent: 25, Min: 5 * time.Minute, Max: 15 * time.Minute},
				{Percent: 50, Min: 15 * time.Minute, Max: 45 * time.Minute},
				{Percent: 20, Min: 45 * time.Minute, Max: 120 * time.Minute},
				{Percent: 5, Min: 2 * time.Hour, Max: 6 * time.Hour},
			},
			Lifecycle: LifecycleDistribution{
				OneShot:  LifecycleBucket{Percent: 60},
				Revisit:  LifecycleBucket{Percent: 25},
				Rotating: LifecycleBucket{Percent: 10, ActiveDuration: DurationRange{Min: 20 * time.Minute, Max: 40 * time.Minute}},
				Long:     LifecycleBucket{Percent: 5, ActiveDuration: DurationRange{Min: 2 * time.Hour, Max: 4 * time.Hour}},
			},
			Payloads:        []PayloadShare{{Percent: 70, Bytes: 256}, {Percent: 25, Bytes: 1_024}, {Percent: 4, Bytes: 4_096}, {Percent: 1, Bytes: 16_384}},
			PersonDirection: PersonDirectionConfig{AlternatingPercent: 70, OneWayPercent: 30},
			Relationship: RelationshipConfig{
				InitialMessages: IntRange{Min: 2, Max: 8}, InitialMessageWindow: DurationRange{Min: 5 * time.Second, Max: 30 * time.Second},
				ReturningMessages: IntRange{Min: 2, Max: 5}, ReturningLast24hPercent: 80, ReturningOlderPercent: 20,
			},
			Retry:  RetryConfig{MaxCount: 3, Delays: []time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}},
			Groups: GroupCatalogConfig{Small: 1_600, Medium: 300, Large: 99, VeryLarge: 1, VeryLargeMembers: formalVeryLargeMembers, FixedMembership: true, VeryLargeSendEvery: time.Minute},
		},
		Observation: ObservationConfig{
			ServiceNodes:    []EndpointDeclaration{{Name: "service-1", Address: "http://service-1.invalid"}, {Name: "service-2", Address: "http://service-2.invalid"}, {Name: "service-3", Address: "http://service-3.invalid"}},
			Workers:         []EndpointDeclaration{{Name: "worker-1", Address: "http://worker-1.invalid"}, {Name: "worker-2", Address: "http://worker-2.invalid"}, {Name: "worker-3", Address: "http://worker-3.invalid"}},
			HostMetrics:     []EndpointDeclaration{{Name: "host-metrics-1", Address: "http://host-metrics-1.invalid"}, {Name: "host-metrics-2", Address: "http://host-metrics-2.invalid"}, {Name: "host-metrics-3", Address: "http://host-metrics-3.invalid"}},
			APIAddrs:        []string{"http://api-1.invalid", "http://api-2.invalid", "http://api-3.invalid"},
			GatewayTCPAddrs: []string{"gateway-1.invalid:5100", "gateway-2.invalid:5100", "gateway-3.invalid:5100"},
			Cadence:         10 * time.Second,
		},
		Thresholds: ThresholdsConfig{
			MinimumDataFilesystemBytes: formalFilesystemBytes, DiskSafeStopFreePercent: 5,
			Correctness: CorrectnessThresholds{OverallFirstAttemptFailure: FailureRateLimit{MaxFailures: 1, PerAttempts: 10_000, Operator: ComparisonLessThan}, AnyMinuteFirstAttemptFailure: FailureRateLimit{MaxFailures: 1, PerAttempts: 1_000, Operator: ComparisonLessOrEqual}},
			Latency:     LatencyThresholds{HotSendACK: LatencyLimit{P99: 200 * time.Millisecond, P999: time.Second}, Cold: LatencyLimit{P99: 2 * time.Second, P999: 5 * time.Second}, Sync: LatencyLimit{P99: time.Second, P999: 3 * time.Second}, SingleAnomaly: 10 * time.Second, SustainedBreachWindow: 5 * time.Minute},
			Resource:    ResourceThresholds{ForcedGCLiveHeapGrowthPercent: 5, ForcedGCLiveHeapWindow: 6 * time.Hour, GoroutineGrowthPercent: 5, GoroutineGrowthWindow: 24 * time.Hour},
			Cluster:     ClusterThresholds{HealthPollEvery: 5 * time.Second, UnhealthyFailAfter: 30 * time.Second, LeaderImbalancePercent: 20, LeaderImbalanceFor: 10 * time.Minute},
			Timeline:    TimelineThresholds{Warmup: 2 * time.Hour, Checkpoint: 24 * time.Hour, Final: formalCheckpointDuration},
		},
		Capacity: CapacityConfig{StartRatePerSecond: 2_000, RecoveryRatePerSecond: 2_000, StepPercent: 25, RefinePercent: 10, Step: CapacityStep{Stabilize: 10 * time.Minute, Measure: 20 * time.Minute}, RecoveryDuration: 30 * time.Minute},
	}
}

// Validate checks static deterministic configuration before planning or I/O.
func (c Config) Validate() error {
	if strings.TrimSpace(c.RunID) == "" {
		return fieldError("run_id", "is required")
	}
	if c.Seed == 0 {
		return fieldError("seed", "must be nonzero")
	}
	if c.Profile != ProfileFormal && c.Profile != ProfileLocal {
		return fieldError("profile", "must be formal or local")
	}
	if c.Mode != ModeSoak && c.Mode != ModeCapacity {
		return fieldError("mode", "must be soak or capacity")
	}
	if err := validateWorkload(c.Workload, c.Profile); err != nil {
		return err
	}
	if err := validateObservation(c.Observation); err != nil {
		return err
	}
	if err := validateThresholds(c.Thresholds); err != nil {
		return err
	}
	if c.Profile == ProfileFormal {
		if err := validateFormalDefaults(c); err != nil {
			return err
		}
	} else if err := validateLocalObservationShape(c.Observation); err != nil {
		return err
	}
	if c.Workload.Workers != len(c.Observation.Workers) {
		return fieldError("workload.workers", "must equal observation worker count")
	}
	return validateCapacity(c.Capacity, c.Profile, c.Mode)
}

func validateWorkload(w WorkloadConfig, profile Profile) error {
	if w.Workers <= 0 {
		return fieldError("workload.workers", "must be greater than zero")
	}
	if w.OnlineUsers <= 0 {
		return fieldError("workload.online_users", "must be greater than zero")
	}
	if w.NewUsersPerDay <= 0 {
		return fieldError("workload.new_users_per_day", "must be greater than zero")
	}
	if w.SendRatePerSecond <= 0 {
		return fieldError("workload.send_rate_per_second", "must be greater than zero")
	}
	if w.HotSet.PersonChannels <= 0 {
		return fieldError("workload.hot_set.person_channels", "must be greater than zero")
	}
	if w.HotSet.GroupChannels <= 0 {
		return fieldError("workload.hot_set.group_channels", "must be greater than zero")
	}
	if w.Topology.LogicalSlotGroups <= 0 {
		return fieldError("workload.topology.logical_slot_groups", "must be greater than zero")
	}
	if w.Topology.HashSlots <= 0 {
		return fieldError("workload.topology.hash_slots", "must be greater than zero")
	}
	if w.Topology.SlotReplicas <= 0 {
		return fieldError("workload.topology.slot_replicas", "must be greater than zero")
	}
	if w.Topology.ChannelReplicas <= 0 {
		return fieldError("workload.topology.channel_replicas", "must be greater than zero")
	}
	if w.RuntimeSampling.Every <= 0 {
		return fieldError("workload.runtime_sampling.every", "must be greater than zero")
	}
	if w.RuntimeSampling.Size <= 0 {
		return fieldError("workload.runtime_sampling.size", "must be greater than zero")
	}
	if w.RuntimeSampling.Size > formalRuntimeSampleSize {
		return fieldError("workload.runtime_sampling.size", "must not exceed 1200")
	}
	if w.Sync.Limit <= 0 {
		return fieldError("workload.sync.limit", "must be greater than zero")
	}
	if w.Sync.MessageCount <= 0 {
		return fieldError("workload.sync.message_count", "must be greater than zero")
	}
	if w.Sync.Version != 0 {
		return fieldError("workload.sync.version", "must equal 0 for real sync")
	}
	if w.Sync.Limit != formalSyncLimit {
		return fieldError("workload.sync.limit", "must equal 500 for real sync")
	}
	if w.Sync.MessageCount != formalSyncMessageCount {
		return fieldError("workload.sync.message_count", "must equal 20 for real sync")
	}
	if w.BurstCredit <= 0 {
		return fieldError("workload.burst_credit", "must be greater than zero")
	}
	if w.MaxGlobalBurst <= 0 {
		return fieldError("workload.max_global_burst", "must be greater than zero")
	}
	if int64(w.BurstCredit)*int64(w.SendRatePerSecond) != int64(w.MaxGlobalBurst)*int64(time.Second) {
		return fieldError("workload.max_global_burst", "must equal burst_credit times send_rate_per_second")
	}
	if w.MaxChannelsPerNode <= 0 {
		return fieldError("workload.max_channels_per_node", "must be greater than zero")
	}
	// The current planner assigns both active person and group hot-set channels
	// against this per-node allocation bound.
	if w.HotSet.PersonChannels+w.HotSet.GroupChannels > w.MaxChannelsPerNode {
		return fieldError("workload.max_channels_per_node", "must cover active person and group hot-set channels")
	}
	if err := validatePercentPair("workload.traffic", w.Traffic.PersonPercent, w.Traffic.GroupPercent); err != nil {
		return err
	}
	if err := validatePercentPair("workload.login", w.Login.NewPercent, w.Login.ReturningPercent); err != nil {
		return err
	}
	if err := validateDurationShares("workload.sessions", w.Sessions, true); err != nil {
		return err
	}
	if err := validateLifecycle(w.Lifecycle); err != nil {
		return err
	}
	if err := validatePayloads(w.Payloads); err != nil {
		return err
	}
	if err := validatePercentPair("workload.person_direction", w.PersonDirection.AlternatingPercent, w.PersonDirection.OneWayPercent); err != nil {
		return err
	}
	if err := validateIntRange("workload.relationship.initial_messages", w.Relationship.InitialMessages); err != nil {
		return err
	}
	if err := validateDurationRange("workload.relationship.initial_message_window", w.Relationship.InitialMessageWindow); err != nil {
		return err
	}
	if err := validateIntRange("workload.relationship.returning_messages", w.Relationship.ReturningMessages); err != nil {
		return err
	}
	if err := validatePercentPair("workload.relationship.returning_age", w.Relationship.ReturningLast24hPercent, w.Relationship.ReturningOlderPercent); err != nil {
		return err
	}
	if w.Retry.MaxCount < 0 {
		return fieldError("workload.retry.max_count", "must not be negative")
	}
	if w.Retry.MaxCount > 3 {
		return fieldError("workload.retry.max_count", "must not exceed 3")
	}
	if len(w.Retry.Delays) != 3 {
		return fieldError("workload.retry.delays", "must contain exactly 3 delays")
	}
	for i, delay := range w.Retry.Delays {
		if delay <= 0 {
			return fieldError(fmt.Sprintf("workload.retry.delays[%d]", i), "must be greater than zero")
		}
	}
	groupCategories := []struct {
		path  string
		count int
	}{
		{"workload.groups.small", w.Groups.Small},
		{"workload.groups.medium", w.Groups.Medium},
		{"workload.groups.large", w.Groups.Large},
		{"workload.groups.very_large", w.Groups.VeryLarge},
	}
	for _, category := range groupCategories {
		if category.count < 0 || category.count > formalGroupCatalogTotal {
			return fieldError(category.path, "must be in 0..2000")
		}
	}
	groupTotal := w.Groups.Small + w.Groups.Medium + w.Groups.Large + w.Groups.VeryLarge
	if groupTotal <= 0 || groupTotal > formalGroupCatalogTotal {
		return fieldError("workload.groups", "catalog total must be in 1..2000")
	}
	if profile == ProfileFormal && groupTotal != formalGroupCatalogTotal {
		return fieldError("workload.groups", "catalog counts must total 2000")
	}
	if profile == ProfileFormal && w.Groups.VeryLarge != 1 {
		return fieldError("workload.groups.very_large", "must equal 1")
	}
	if profile == ProfileFormal && w.Groups.VeryLargeMembers != formalVeryLargeMembers {
		return fieldError("workload.groups.very_large_members", "must equal 100000")
	}
	if w.HotSet.GroupChannels != groupTotal {
		return fieldError("workload.hot_set.group_channels", "must equal group catalog total")
	}
	if !w.Groups.FixedMembership {
		return fieldError("workload.groups.fixed_membership", "must be true")
	}
	if w.Groups.VeryLarge > 0 {
		if w.Groups.VeryLargeMembers <= 0 {
			return fieldError("workload.groups.very_large_members", "must be greater than zero when very_large is positive")
		}
		if w.Groups.VeryLargeSendEvery <= 0 {
			return fieldError("workload.groups.very_large_send_every", "must be greater than zero when very_large is positive")
		}
	} else {
		if w.Groups.VeryLargeMembers != 0 {
			return fieldError("workload.groups.very_large_members", "must be zero when very_large is zero")
		}
		if w.Groups.VeryLargeSendEvery != 0 {
			return fieldError("workload.groups.very_large_send_every", "must be zero when very_large is zero")
		}
	}
	if w.Topology.LogicalSlotGroups != formalLogicalSlotGroups || w.Topology.HashSlots != formalHashSlots || w.Topology.SlotReplicas != formalReplicas || w.Topology.ChannelReplicas != formalReplicas {
		return fieldError("workload.topology", "must preserve 12 logical slot groups, 256 hash slots, and 3 replicas")
	}
	return nil
}

func validateLocalObservationShape(o ObservationConfig) error {
	roles := []struct {
		path  string
		count int
	}{
		{"observation.service_nodes", len(o.ServiceNodes)},
		{"observation.workers", len(o.Workers)},
		{"observation.host_metrics", len(o.HostMetrics)},
		{"observation.api_addrs", len(o.APIAddrs)},
		{"observation.gateway_tcp_addrs", len(o.GatewayTCPAddrs)},
	}
	for _, role := range roles {
		if role.count != formalWorkers {
			return fieldError(role.path, "must contain exactly 3 entries for local baseline")
		}
	}
	return nil
}

func validateObservation(o ObservationConfig) error {
	if o.Cadence <= 0 {
		return fieldError("observation.cadence", "must be greater than zero")
	}
	if err := validateEndpointRole("observation.service_nodes", o.ServiceNodes); err != nil {
		return err
	}
	if err := validateEndpointRole("observation.workers", o.Workers); err != nil {
		return err
	}
	if err := validateEndpointRole("observation.host_metrics", o.HostMetrics); err != nil {
		return err
	}
	if err := validateHTTPAddressPool("observation.api_addrs", o.APIAddrs); err != nil {
		return err
	}
	if err := validateGatewayAddressPool("observation.gateway_tcp_addrs", o.GatewayTCPAddrs); err != nil {
		return err
	}
	if err := validateCrossRoleEndpointDuplicates(o); err != nil {
		return err
	}
	for gatewayIndex, gateway := range o.GatewayTCPAddrs {
		gatewayKey, _ := parseGatewayEndpoint(gateway)
		for apiIndex, api := range o.APIAddrs {
			apiKey, _ := parseHTTPEndpoint(api)
			if gatewayKey == apiKey.authority {
				return fieldError(fmt.Sprintf("observation.gateway_tcp_addrs[%d]", gatewayIndex), fmt.Sprintf("aliases observation.api_addrs[%d]", apiIndex))
			}
		}
	}
	return nil
}

func validateEndpointRole(path string, endpoints []EndpointDeclaration) error {
	if len(endpoints) == 0 {
		return fieldError(path, "must not be empty")
	}
	seenNames := make(map[string]int, len(endpoints))
	seenAddresses := make(map[string]int, len(endpoints))
	for i, endpoint := range endpoints {
		name := strings.TrimSpace(endpoint.Name)
		if name == "" {
			return fieldError(fmt.Sprintf("%s[%d].name", path, i), "is required")
		}
		if previous, ok := seenNames[name]; ok {
			return fieldError(fmt.Sprintf("%s[%d].name", path, i), fmt.Sprintf("duplicates %s[%d].name", path, previous))
		}
		address := strings.TrimSpace(endpoint.Address)
		if address == "" {
			return fieldError(fmt.Sprintf("%s[%d].address", path, i), "is required")
		}
		parsed, reason := parseHTTPEndpoint(address)
		if reason != "" {
			return fieldError(fmt.Sprintf("%s[%d].address", path, i), reason)
		}
		if previous, ok := seenAddresses[parsed.key]; ok {
			return fieldError(fmt.Sprintf("%s[%d].address", path, i), fmt.Sprintf("duplicates %s[%d].address", path, previous))
		}
		seenNames[name], seenAddresses[parsed.key] = i, i
	}
	return nil
}

func validateHTTPAddressPool(path string, addresses []string) error {
	if len(addresses) == 0 {
		return fieldError(path, "must not be empty")
	}
	seen := make(map[string]int, len(addresses))
	for i, raw := range addresses {
		address := strings.TrimSpace(raw)
		if address == "" {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), "is required")
		}
		parsed, reason := parseHTTPEndpoint(address)
		if reason != "" {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), reason)
		}
		if previous, ok := seen[parsed.key]; ok {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), fmt.Sprintf("duplicates %s[%d]", path, previous))
		}
		seen[parsed.key] = i
	}
	return nil
}

func validateGatewayAddressPool(path string, addresses []string) error {
	if len(addresses) == 0 {
		return fieldError(path, "must not be empty")
	}
	seen := make(map[string]int, len(addresses))
	for i, raw := range addresses {
		if strings.TrimSpace(raw) == "" {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), "is required")
		}
		key, reason := parseGatewayEndpoint(raw)
		if reason != "" {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), reason)
		}
		if previous, ok := seen[key]; ok {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), fmt.Sprintf("duplicates %s[%d]", path, previous))
		}
		seen[key] = i
	}
	return nil
}

func validateCrossRoleEndpointDuplicates(o ObservationConfig) error {
	roles := []struct {
		path      string
		endpoints []EndpointDeclaration
	}{
		{"observation.service_nodes", o.ServiceNodes},
		{"observation.workers", o.Workers},
		{"observation.host_metrics", o.HostMetrics},
	}
	seenNames := make(map[string]string)
	seenAddresses := make(map[string]string)
	for _, role := range roles {
		for index, endpoint := range role.endpoints {
			namePath := fmt.Sprintf("%s[%d].name", role.path, index)
			name := strings.TrimSpace(endpoint.Name)
			if previous, ok := seenNames[name]; ok {
				return fieldError(namePath, "duplicates "+previous)
			}
			seenNames[name] = namePath
			addressPath := fmt.Sprintf("%s[%d].address", role.path, index)
			address, _ := parseHTTPEndpoint(endpoint.Address)
			if previous, ok := seenAddresses[address.key]; ok {
				return fieldError(addressPath, "duplicates "+previous)
			}
			seenAddresses[address.key] = addressPath
		}
	}
	return nil
}

type httpEndpointKey struct {
	key       string
	authority string
}

func parseHTTPEndpoint(raw string) (httpEndpointKey, string) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		if strings.Contains(err.Error(), "invalid port") {
			return httpEndpointKey{}, "port must be a number in 1..65535"
		}
		return httpEndpointKey{}, "must be a valid absolute HTTP URL"
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "" || !parsed.IsAbs() {
		return httpEndpointKey{}, "must be a valid absolute HTTP URL"
	}
	if scheme != "http" && scheme != "https" {
		return httpEndpointKey{}, "scheme must be http or https"
	}
	if parsed.User != nil {
		return httpEndpointKey{}, "must not include userinfo"
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return httpEndpointKey{}, "must not include a query"
	}
	if parsed.Fragment != "" {
		return httpEndpointKey{}, "must not include a fragment"
	}
	host := parsed.Hostname()
	if host == "" {
		return httpEndpointKey{}, "host is required"
	}
	port := parsed.Port()
	if strings.HasSuffix(parsed.Host, ":") {
		return httpEndpointKey{}, "port must be a number in 1..65535"
	}
	if port == "" {
		if scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return httpEndpointKey{}, "port must be a number in 1..65535"
	}
	authority := net.JoinHostPort(canonicalEndpointHost(host), strconv.Itoa(portNumber))
	basePath := pathpkg.Clean(parsed.EscapedPath())
	if basePath == "." || basePath == "/" {
		basePath = ""
	}
	return httpEndpointKey{
		key:       scheme + "://" + authority + basePath,
		authority: authority,
	}, ""
}

func parseGatewayEndpoint(raw string) (string, string) {
	address := strings.TrimSpace(raw)
	if strings.Contains(address, "://") {
		return "", "must be a TCP host:port"
	}
	if strings.Contains(address, "@") {
		return "", "must not include userinfo"
	}
	if strings.ContainsAny(address, "/?#") {
		return "", "must not include a path, query, or fragment"
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", "must be a TCP host:port"
	}
	if host == "" {
		return "", "host is required"
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", "port must be a number in 1..65535"
	}
	return net.JoinHostPort(canonicalEndpointHost(host), strconv.Itoa(portNumber)), ""
}

func canonicalEndpointHost(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return strings.ToLower(host)
}

func validateThresholds(t ThresholdsConfig) error {
	if t.MinimumDataFilesystemBytes <= 0 {
		return fieldError("thresholds.minimum_data_filesystem_bytes", "must be greater than zero")
	}
	if t.DiskSafeStopFreePercent < 1 || t.DiskSafeStopFreePercent > 100 {
		return fieldError("thresholds.disk_safe_stop_free_percent", "must be in 1..100")
	}
	correctness := []struct {
		path  string
		value int
	}{
		{"thresholds.correctness.terminal_sends", t.Correctness.TerminalSends},
		{"thresholds.correctness.activation_rejections", t.Correctness.ActivationRejections},
		{"thresholds.correctness.losses", t.Correctness.Losses},
		{"thresholds.correctness.duplicates", t.Correctness.Duplicates},
		{"thresholds.correctness.corruptions", t.Correctness.Corruptions},
		{"thresholds.correctness.sequence_regressions", t.Correctness.SequenceRegressions},
	}
	for _, entry := range correctness {
		if entry.value < 0 {
			return fieldError(entry.path, "must not be negative")
		}
	}
	if err := validateFailureRate("thresholds.correctness.overall_first_attempt_failure", t.Correctness.OverallFirstAttemptFailure); err != nil {
		return err
	}
	if err := validateFailureRate("thresholds.correctness.any_minute_first_attempt_failure", t.Correctness.AnyMinuteFirstAttemptFailure); err != nil {
		return err
	}
	if err := validateLatencyLimit("thresholds.latency.hot_sendack", t.Latency.HotSendACK); err != nil {
		return err
	}
	if err := validateLatencyLimit("thresholds.latency.cold", t.Latency.Cold); err != nil {
		return err
	}
	if err := validateLatencyLimit("thresholds.latency.sync", t.Latency.Sync); err != nil {
		return err
	}
	if t.Latency.SingleAnomaly <= 0 {
		return fieldError("thresholds.latency.single_anomaly", "must be greater than zero")
	}
	if t.Latency.SustainedBreachWindow <= 0 {
		return fieldError("thresholds.latency.sustained_breach_window", "must be greater than zero")
	}
	if t.Resource.ForcedGCLiveHeapGrowthPercent < 0 || t.Resource.ForcedGCLiveHeapGrowthPercent > 100 {
		return fieldError("thresholds.resource.forced_gc_live_heap_growth_percent", "must be in 0..100")
	}
	if t.Resource.ForcedGCLiveHeapWindow <= 0 {
		return fieldError("thresholds.resource.forced_gc_live_heap_window", "must be greater than zero")
	}
	if t.Resource.GoroutineGrowthPercent < 0 || t.Resource.GoroutineGrowthPercent > 100 {
		return fieldError("thresholds.resource.goroutine_growth_percent", "must be in 0..100")
	}
	if t.Resource.GoroutineGrowthWindow <= 0 {
		return fieldError("thresholds.resource.goroutine_growth_window", "must be greater than zero")
	}
	if t.Cluster.HealthPollEvery <= 0 {
		return fieldError("thresholds.cluster.health_poll_every", "must be greater than zero")
	}
	if t.Cluster.UnhealthyFailAfter <= 0 {
		return fieldError("thresholds.cluster.unhealthy_fail_after", "must be greater than zero")
	}
	if t.Cluster.LeaderImbalancePercent < 0 || t.Cluster.LeaderImbalancePercent > 100 {
		return fieldError("thresholds.cluster.leader_imbalance_percent", "must be in 0..100")
	}
	if t.Cluster.LeaderImbalanceFor <= 0 {
		return fieldError("thresholds.cluster.leader_imbalance_for", "must be greater than zero")
	}
	if t.Timeline.Warmup <= 0 {
		return fieldError("thresholds.timeline.warmup", "must be greater than zero")
	}
	if t.Timeline.Checkpoint <= 0 {
		return fieldError("thresholds.timeline.checkpoint", "must be greater than zero")
	}
	if t.Timeline.Final <= 0 {
		return fieldError("thresholds.timeline.final", "must be greater than zero")
	}
	if t.Timeline.Warmup >= t.Timeline.Checkpoint {
		return fieldError("thresholds.timeline.warmup", "must be before checkpoint")
	}
	if t.Timeline.Checkpoint >= t.Timeline.Final {
		return fieldError("thresholds.timeline.checkpoint", "must be before final")
	}
	return nil
}

func validateCapacity(c CapacityConfig, profile Profile, mode Mode) error {
	if c.StartRatePerSecond <= 0 {
		return fieldError("capacity.start_rate_per_second", "must be greater than zero")
	}
	if c.RecoveryRatePerSecond <= 0 {
		return fieldError("capacity.recovery_rate_per_second", "must be greater than zero")
	}
	if c.StepPercent < 1 || c.StepPercent > 100 {
		return fieldError("capacity.step_percent", "must be in 1..100")
	}
	if c.RefinePercent < 1 || c.RefinePercent > 100 {
		return fieldError("capacity.refine_percent", "must be in 1..100")
	}
	if c.Step.Stabilize <= 0 {
		return fieldError("capacity.step.stabilize", "must be greater than zero")
	}
	if c.Step.Measure <= 0 {
		return fieldError("capacity.step.measure", "must be greater than zero")
	}
	if c.RecoveryDuration <= 0 {
		return fieldError("capacity.recovery_duration", "must be greater than zero")
	}
	if profile == ProfileFormal && mode != ModeCapacity {
		if err := validateFormalCapacity(c); err != nil {
			return err
		}
	}
	if c.Step.Stabilize+c.Step.Measure != capacityStepDuration {
		return fieldError("capacity.step", "stabilize plus measure must equal 30m0s")
	}
	if mode != ModeCapacity {
		return nil
	}
	if profile != ProfileFormal {
		return fieldError("profile", "must be formal in capacity mode")
	}
	if err := validateFormalCapacity(c); err != nil {
		return err
	}
	if strings.TrimSpace(c.AgedCheckpoint.Reference) == "" {
		return fieldError("capacity.aged_checkpoint.reference", "is required in capacity profile")
	}
	if !c.AgedCheckpoint.Completed {
		return fieldError("capacity.aged_checkpoint.completed", "must be true in capacity profile")
	}
	if !c.AgedCheckpoint.Passed {
		return fieldError("capacity.aged_checkpoint.passed", "must be true in capacity profile")
	}
	if c.AgedCheckpoint.Duration < formalCheckpointDuration {
		return fieldError("capacity.aged_checkpoint.duration", "must be at least 72h0m0s in capacity profile")
	}
	return nil
}

func validateFormalCapacity(c CapacityConfig) error {
	expected := FormalConfig().Capacity
	if c.StartRatePerSecond != expected.StartRatePerSecond {
		return formalError("capacity.start_rate_per_second")
	}
	if c.RecoveryRatePerSecond != expected.RecoveryRatePerSecond {
		return formalError("capacity.recovery_rate_per_second")
	}
	if c.StepPercent != expected.StepPercent {
		return formalError("capacity.step_percent")
	}
	if c.RefinePercent != expected.RefinePercent {
		return formalError("capacity.refine_percent")
	}
	if c.Step.Stabilize != expected.Step.Stabilize {
		return formalError("capacity.step.stabilize")
	}
	if c.Step.Measure != expected.Step.Measure {
		return formalError("capacity.step.measure")
	}
	if c.RecoveryDuration != expected.RecoveryDuration {
		return formalError("capacity.recovery_duration")
	}
	return nil
}

func validateFormalDefaults(c Config) error {
	expected := FormalConfig()
	w, wantW := c.Workload, expected.Workload
	if w.Workers != wantW.Workers {
		return formalError("workload.workers")
	}
	if w.OnlineUsers != wantW.OnlineUsers {
		return formalError("workload.online_users")
	}
	if w.NewUsersPerDay != wantW.NewUsersPerDay {
		return formalError("workload.new_users_per_day")
	}
	if w.SendRatePerSecond != wantW.SendRatePerSecond {
		return formalError("workload.send_rate_per_second")
	}
	if w.Traffic.PersonPercent != wantW.Traffic.PersonPercent {
		return formalError("workload.traffic.person_percent")
	}
	if w.Traffic.GroupPercent != wantW.Traffic.GroupPercent {
		return formalError("workload.traffic.group_percent")
	}
	if w.HotSet.PersonChannels != wantW.HotSet.PersonChannels {
		return formalError("workload.hot_set.person_channels")
	}
	if w.HotSet.GroupChannels != wantW.HotSet.GroupChannels {
		return formalError("workload.hot_set.group_channels")
	}
	if w.Topology.LogicalSlotGroups != wantW.Topology.LogicalSlotGroups {
		return formalError("workload.topology.logical_slot_groups")
	}
	if w.Topology.HashSlots != wantW.Topology.HashSlots {
		return formalError("workload.topology.hash_slots")
	}
	if w.Topology.SlotReplicas != wantW.Topology.SlotReplicas {
		return formalError("workload.topology.slot_replicas")
	}
	if w.Topology.ChannelReplicas != wantW.Topology.ChannelReplicas {
		return formalError("workload.topology.channel_replicas")
	}
	if w.RuntimeSampling.Every != wantW.RuntimeSampling.Every {
		return formalError("workload.runtime_sampling.every")
	}
	if w.RuntimeSampling.Size != wantW.RuntimeSampling.Size {
		return formalError("workload.runtime_sampling.size")
	}
	if w.Sync.Version != wantW.Sync.Version {
		return formalError("workload.sync.version")
	}
	if w.Sync.Limit != wantW.Sync.Limit {
		return formalError("workload.sync.limit")
	}
	if w.Sync.MessageCount != wantW.Sync.MessageCount {
		return formalError("workload.sync.message_count")
	}
	if w.BurstCredit != wantW.BurstCredit {
		return formalError("workload.burst_credit")
	}
	if w.MaxGlobalBurst != wantW.MaxGlobalBurst {
		return formalError("workload.max_global_burst")
	}
	if w.MaxChannelsPerNode != wantW.MaxChannelsPerNode {
		return formalError("workload.max_channels_per_node")
	}
	if w.Login.NewPercent != wantW.Login.NewPercent {
		return formalError("workload.login.new_percent")
	}
	if w.Login.ReturningPercent != wantW.Login.ReturningPercent {
		return formalError("workload.login.returning_percent")
	}
	if err := validateExactDurationShares("workload.sessions", w.Sessions, wantW.Sessions); err != nil {
		return err
	}
	if err := validateExactLifecycle(w.Lifecycle, wantW.Lifecycle); err != nil {
		return err
	}
	if err := validateExactPayloads(w.Payloads, wantW.Payloads); err != nil {
		return err
	}
	if w.PersonDirection.AlternatingPercent != wantW.PersonDirection.AlternatingPercent {
		return formalError("workload.person_direction.alternating_percent")
	}
	if w.PersonDirection.OneWayPercent != wantW.PersonDirection.OneWayPercent {
		return formalError("workload.person_direction.one_way_percent")
	}
	if err := validateExactIntRange("workload.relationship.initial_messages", w.Relationship.InitialMessages, wantW.Relationship.InitialMessages); err != nil {
		return err
	}
	if err := validateExactDurationRange("workload.relationship.initial_message_window", w.Relationship.InitialMessageWindow, wantW.Relationship.InitialMessageWindow); err != nil {
		return err
	}
	if err := validateExactIntRange("workload.relationship.returning_messages", w.Relationship.ReturningMessages, wantW.Relationship.ReturningMessages); err != nil {
		return err
	}
	if w.Relationship.ReturningLast24hPercent != wantW.Relationship.ReturningLast24hPercent {
		return formalError("workload.relationship.returning_last_24h_percent")
	}
	if w.Relationship.ReturningOlderPercent != wantW.Relationship.ReturningOlderPercent {
		return formalError("workload.relationship.returning_older_percent")
	}
	if w.Retry.MaxCount != wantW.Retry.MaxCount {
		return formalError("workload.retry.max_count")
	}
	for i := range wantW.Retry.Delays {
		if len(w.Retry.Delays) <= i || w.Retry.Delays[i] != wantW.Retry.Delays[i] {
			return formalError(fmt.Sprintf("workload.retry.delays[%d]", i))
		}
	}
	if w.Groups.Small != wantW.Groups.Small {
		return formalError("workload.groups.small")
	}
	if w.Groups.Medium != wantW.Groups.Medium {
		return formalError("workload.groups.medium")
	}
	if w.Groups.Large != wantW.Groups.Large {
		return formalError("workload.groups.large")
	}
	if w.Groups.VeryLarge != wantW.Groups.VeryLarge {
		return formalError("workload.groups.very_large")
	}
	if w.Groups.VeryLargeMembers != wantW.Groups.VeryLargeMembers {
		return formalError("workload.groups.very_large_members")
	}
	if w.Groups.FixedMembership != wantW.Groups.FixedMembership {
		return formalError("workload.groups.fixed_membership")
	}
	if w.Groups.VeryLargeSendEvery != wantW.Groups.VeryLargeSendEvery {
		return formalError("workload.groups.very_large_send_every")
	}
	if err := validateExactObservation(c.Observation, expected.Observation); err != nil {
		return err
	}
	if err := validateExactThresholds(c.Thresholds, expected.Thresholds); err != nil {
		return err
	}
	return nil
}

func validateExactDurationShares(path string, got, want []DurationShare) error {
	if len(got) != len(want) {
		return formalError(path)
	}
	for i := range want {
		if got[i].Percent != want[i].Percent {
			return formalError(fmt.Sprintf("%s[%d].percent", path, i))
		}
		if got[i].Min != want[i].Min {
			return formalError(fmt.Sprintf("%s[%d].min", path, i))
		}
		if got[i].Max != want[i].Max {
			return formalError(fmt.Sprintf("%s[%d].max", path, i))
		}
	}
	return nil
}

func validateExactLifecycle(got, want LifecycleDistribution) error {
	entries := []struct {
		path      string
		got, want LifecycleBucket
	}{
		{"workload.lifecycle.one_shot", got.OneShot, want.OneShot}, {"workload.lifecycle.revisit", got.Revisit, want.Revisit},
		{"workload.lifecycle.rotating", got.Rotating, want.Rotating}, {"workload.lifecycle.long", got.Long, want.Long},
	}
	for _, entry := range entries {
		if entry.got.Percent != entry.want.Percent {
			return formalError(entry.path + ".percent")
		}
		if err := validateExactDurationRange(entry.path+".active_duration", entry.got.ActiveDuration, entry.want.ActiveDuration); err != nil {
			return err
		}
	}
	return nil
}

func validateExactPayloads(got, want []PayloadShare) error {
	if len(got) != len(want) {
		return formalError("workload.payloads")
	}
	for i := range want {
		if got[i].Percent != want[i].Percent {
			return formalError(fmt.Sprintf("workload.payloads[%d].percent", i))
		}
		if got[i].Bytes != want[i].Bytes {
			return formalError(fmt.Sprintf("workload.payloads[%d].bytes", i))
		}
	}
	return nil
}

func validateExactIntRange(path string, got, want IntRange) error {
	if got.Min != want.Min {
		return formalError(path + ".min")
	}
	if got.Max != want.Max {
		return formalError(path + ".max")
	}
	return nil
}

func validateExactDurationRange(path string, got, want DurationRange) error {
	if got.Min != want.Min {
		return formalError(path + ".min")
	}
	if got.Max != want.Max {
		return formalError(path + ".max")
	}
	return nil
}

func validateExactObservation(got, want ObservationConfig) error {
	if got.Cadence != want.Cadence {
		return formalError("observation.cadence")
	}
	if len(got.ServiceNodes) != len(want.ServiceNodes) {
		return formalError("observation.service_nodes")
	}
	if len(got.Workers) != len(want.Workers) {
		return formalError("observation.workers")
	}
	if len(got.HostMetrics) != len(want.HostMetrics) {
		return formalError("observation.host_metrics")
	}
	if len(got.APIAddrs) != len(want.APIAddrs) {
		return formalError("observation.api_addrs")
	}
	if len(got.GatewayTCPAddrs) != len(want.GatewayTCPAddrs) {
		return formalError("observation.gateway_tcp_addrs")
	}
	return nil
}

func validateExactThresholds(got, want ThresholdsConfig) error {
	if got.MinimumDataFilesystemBytes != want.MinimumDataFilesystemBytes {
		return formalError("thresholds.minimum_data_filesystem_bytes")
	}
	if got.DiskSafeStopFreePercent != want.DiskSafeStopFreePercent {
		return formalError("thresholds.disk_safe_stop_free_percent")
	}
	correctness := []struct {
		path      string
		got, want int
	}{
		{"thresholds.correctness.terminal_sends", got.Correctness.TerminalSends, want.Correctness.TerminalSends}, {"thresholds.correctness.activation_rejections", got.Correctness.ActivationRejections, want.Correctness.ActivationRejections},
		{"thresholds.correctness.losses", got.Correctness.Losses, want.Correctness.Losses}, {"thresholds.correctness.duplicates", got.Correctness.Duplicates, want.Correctness.Duplicates},
		{"thresholds.correctness.corruptions", got.Correctness.Corruptions, want.Correctness.Corruptions}, {"thresholds.correctness.sequence_regressions", got.Correctness.SequenceRegressions, want.Correctness.SequenceRegressions},
	}
	for _, entry := range correctness {
		if entry.got != entry.want {
			return formalError(entry.path)
		}
	}
	if err := validateExactFailureRate("thresholds.correctness.overall_first_attempt_failure", got.Correctness.OverallFirstAttemptFailure, want.Correctness.OverallFirstAttemptFailure); err != nil {
		return err
	}
	if err := validateExactFailureRate("thresholds.correctness.any_minute_first_attempt_failure", got.Correctness.AnyMinuteFirstAttemptFailure, want.Correctness.AnyMinuteFirstAttemptFailure); err != nil {
		return err
	}
	if err := validateExactLatency("thresholds.latency.hot_sendack", got.Latency.HotSendACK, want.Latency.HotSendACK); err != nil {
		return err
	}
	if err := validateExactLatency("thresholds.latency.cold", got.Latency.Cold, want.Latency.Cold); err != nil {
		return err
	}
	if err := validateExactLatency("thresholds.latency.sync", got.Latency.Sync, want.Latency.Sync); err != nil {
		return err
	}
	if got.Latency.SingleAnomaly != want.Latency.SingleAnomaly {
		return formalError("thresholds.latency.single_anomaly")
	}
	if got.Latency.SustainedBreachWindow != want.Latency.SustainedBreachWindow {
		return formalError("thresholds.latency.sustained_breach_window")
	}
	if got.Resource.ForcedGCLiveHeapGrowthPercent != want.Resource.ForcedGCLiveHeapGrowthPercent {
		return formalError("thresholds.resource.forced_gc_live_heap_growth_percent")
	}
	if got.Resource.ForcedGCLiveHeapWindow != want.Resource.ForcedGCLiveHeapWindow {
		return formalError("thresholds.resource.forced_gc_live_heap_window")
	}
	if got.Resource.GoroutineGrowthPercent != want.Resource.GoroutineGrowthPercent {
		return formalError("thresholds.resource.goroutine_growth_percent")
	}
	if got.Resource.GoroutineGrowthWindow != want.Resource.GoroutineGrowthWindow {
		return formalError("thresholds.resource.goroutine_growth_window")
	}
	if got.Cluster.HealthPollEvery != want.Cluster.HealthPollEvery {
		return formalError("thresholds.cluster.health_poll_every")
	}
	if got.Cluster.UnhealthyFailAfter != want.Cluster.UnhealthyFailAfter {
		return formalError("thresholds.cluster.unhealthy_fail_after")
	}
	if got.Cluster.LeaderImbalancePercent != want.Cluster.LeaderImbalancePercent {
		return formalError("thresholds.cluster.leader_imbalance_percent")
	}
	if got.Cluster.LeaderImbalanceFor != want.Cluster.LeaderImbalanceFor {
		return formalError("thresholds.cluster.leader_imbalance_for")
	}
	if got.Timeline.Warmup != want.Timeline.Warmup {
		return formalError("thresholds.timeline.warmup")
	}
	if got.Timeline.Checkpoint != want.Timeline.Checkpoint {
		return formalError("thresholds.timeline.checkpoint")
	}
	if got.Timeline.Final != want.Timeline.Final {
		return formalError("thresholds.timeline.final")
	}
	return nil
}

func validateExactFailureRate(path string, got, want FailureRateLimit) error {
	if got.MaxFailures != want.MaxFailures {
		return formalError(path + ".max_failures")
	}
	if got.PerAttempts != want.PerAttempts {
		return formalError(path + ".per_attempts")
	}
	if got.Operator != want.Operator {
		return formalError(path + ".operator")
	}
	return nil
}

func validateExactLatency(path string, got, want LatencyLimit) error {
	if got.P99 != want.P99 {
		return formalError(path + ".p99")
	}
	if got.P999 != want.P999 {
		return formalError(path + ".p999")
	}
	return nil
}

func validatePercentPair(path string, first, second int) error {
	if first < 0 || first > 100 || second < 0 || second > 100 {
		return fieldError(path, "percentages must be in 0..100")
	}
	if first+second != 100 {
		return fieldError(path, "percentages must total 100")
	}
	return nil
}

func validateDurationShares(path string, shares []DurationShare, requireRange bool) error {
	if len(shares) == 0 {
		return fieldError(path, "must not be empty")
	}
	total := 0
	for i, share := range shares {
		if share.Percent < 0 || share.Percent > 100 {
			return fieldError(fmt.Sprintf("%s[%d].percent", path, i), "must be in 0..100")
		}
		total += share.Percent
		if share.Min == 0 && share.Max == 0 && !requireRange {
			continue
		}
		if share.Min <= 0 {
			return fieldError(fmt.Sprintf("%s[%d].min", path, i), "must be greater than zero")
		}
		if share.Max <= 0 {
			return fieldError(fmt.Sprintf("%s[%d].max", path, i), "must be greater than zero")
		}
		if share.Min > share.Max {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), "min must not exceed max")
		}
	}
	if total != 100 {
		return fieldError(path, "percentages must total 100")
	}
	return nil
}

func validateLifecycle(lifecycle LifecycleDistribution) error {
	buckets := []struct {
		path          string
		bucket        LifecycleBucket
		requiresRange bool
	}{
		{"workload.lifecycle.one_shot", lifecycle.OneShot, false},
		{"workload.lifecycle.revisit", lifecycle.Revisit, false},
		{"workload.lifecycle.rotating", lifecycle.Rotating, true},
		{"workload.lifecycle.long", lifecycle.Long, true},
	}
	total := 0
	for _, entry := range buckets {
		if entry.bucket.Percent < 0 || entry.bucket.Percent > 100 {
			return fieldError(entry.path+".percent", "must be in 0..100")
		}
		total += entry.bucket.Percent
		rangeValue := entry.bucket.ActiveDuration
		if !entry.requiresRange {
			if rangeValue.Min != 0 || rangeValue.Max != 0 {
				return fieldError(entry.path+".active_duration", "must be empty")
			}
			continue
		}
		if rangeValue.Min <= 0 {
			return fieldError(entry.path+".active_duration.min", "must be greater than zero")
		}
		if rangeValue.Max <= 0 {
			return fieldError(entry.path+".active_duration.max", "must be greater than zero")
		}
		if rangeValue.Min > rangeValue.Max {
			return fieldError(entry.path+".active_duration", "min must not exceed max")
		}
	}
	if total != 100 {
		return fieldError("workload.lifecycle", "percentages must total 100")
	}
	return nil
}

func validatePayloads(shares []PayloadShare) error {
	if len(shares) == 0 {
		return fieldError("workload.payloads", "must not be empty")
	}
	total := 0
	for i, share := range shares {
		if share.Percent < 0 || share.Percent > 100 {
			return fieldError(fmt.Sprintf("workload.payloads[%d].percent", i), "must be in 0..100")
		}
		if share.Bytes <= 0 {
			return fieldError(fmt.Sprintf("workload.payloads[%d].bytes", i), "must be greater than zero")
		}
		total += share.Percent
	}
	if total != 100 {
		return fieldError("workload.payloads", "percentages must total 100")
	}
	return nil
}

func validateIntRange(path string, r IntRange) error {
	if r.Min <= 0 {
		return fieldError(path+".min", "must be greater than zero")
	}
	if r.Max <= 0 {
		return fieldError(path+".max", "must be greater than zero")
	}
	if r.Min > r.Max {
		return fieldError(path, "min must not exceed max")
	}
	return nil
}

func validateDurationRange(path string, r DurationRange) error {
	if r.Min <= 0 {
		return fieldError(path+".min", "must be greater than zero")
	}
	if r.Max <= 0 {
		return fieldError(path+".max", "must be greater than zero")
	}
	if r.Min > r.Max {
		return fieldError(path, "min must not exceed max")
	}
	return nil
}

func validateFailureRate(path string, limit FailureRateLimit) error {
	if limit.PerAttempts == 0 {
		return fieldError(path+".per_attempts", "must be greater than zero")
	}
	if limit.MaxFailures > limit.PerAttempts {
		return fieldError(path+".max_failures", "must not exceed per_attempts")
	}
	if limit.Operator != ComparisonLessThan && limit.Operator != ComparisonLessOrEqual {
		return fieldError(path+".operator", "must be < or <=")
	}
	if limit.Operator == ComparisonLessThan && limit.MaxFailures == 0 {
		return fieldError(path+".max_failures", "must be greater than zero when operator is <")
	}
	return nil
}

func validateLatencyLimit(path string, limit LatencyLimit) error {
	if limit.P99 <= 0 {
		return fieldError(path+".p99", "must be greater than zero")
	}
	if limit.P999 <= 0 {
		return fieldError(path+".p999", "must be greater than zero")
	}
	if limit.P99 > limit.P999 {
		return fieldError(path, "p99 must not exceed p999")
	}
	return nil
}

func fieldError(path, reason string) error { return fmt.Errorf("%s: %s", path, reason) }

func formalError(path string) error { return fieldError(path, "must equal formal default") }
