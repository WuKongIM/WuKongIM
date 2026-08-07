package chatlifecycle

import (
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
	formalFilesystemBytes    = int64(500_000_000_000)
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
		ServiceNodes: []EndpointDeclaration{{Name: "local-service-1", Address: "http://127.0.0.1:15001"}, {Name: "local-service-2", Address: "http://127.0.0.1:15002"}, {Name: "local-service-3", Address: "http://127.0.0.1:15003"}},
		Workers:      []EndpointDeclaration{{Name: "local-worker-1", Address: "http://127.0.0.1:19091"}, {Name: "local-worker-2", Address: "http://127.0.0.1:19092"}, {Name: "local-worker-3", Address: "http://127.0.0.1:19093"}},
		HostMetrics: []EndpointDeclaration{
			{Name: "local-host-metrics-1", Address: "http://127.0.0.1:19101", Mountpoint: "/var/lib/wukongim-1", Device: "/dev/local-data-1"},
			{Name: "local-host-metrics-2", Address: "http://127.0.0.1:19102", Mountpoint: "/var/lib/wukongim-2", Device: "/dev/local-data-2"},
			{Name: "local-host-metrics-3", Address: "http://127.0.0.1:19103", Mountpoint: "/var/lib/wukongim-3", Device: "/dev/local-data-3"},
		},
		APIAddrs:        []string{"http://127.0.0.1:15011", "http://127.0.0.1:15012", "http://127.0.0.1:15013"},
		GatewayTCPAddrs: []string{"127.0.0.1:15101", "127.0.0.1:15102", "127.0.0.1:15103"},
		Cadence:         5 * time.Second,
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
			ServiceNodes: []EndpointDeclaration{{Name: "service-1", Address: "http://service-1.invalid"}, {Name: "service-2", Address: "http://service-2.invalid"}, {Name: "service-3", Address: "http://service-3.invalid"}},
			Workers:      []EndpointDeclaration{{Name: "worker-1", Address: "http://worker-1.invalid"}, {Name: "worker-2", Address: "http://worker-2.invalid"}, {Name: "worker-3", Address: "http://worker-3.invalid"}},
			HostMetrics: []EndpointDeclaration{
				{Name: "host-metrics-1", Address: "http://host-metrics-1.invalid", Mountpoint: "/var/lib/wukongim-cloud", Device: "/dev/wukongim-data"},
				{Name: "host-metrics-2", Address: "http://host-metrics-2.invalid", Mountpoint: "/var/lib/wukongim-cloud", Device: "/dev/wukongim-data"},
				{Name: "host-metrics-3", Address: "http://host-metrics-3.invalid", Mountpoint: "/var/lib/wukongim-cloud", Device: "/dev/wukongim-data"},
			},
			APIAddrs:        []string{"http://api-1.invalid", "http://api-2.invalid", "http://api-3.invalid"},
			GatewayTCPAddrs: []string{"gateway-1.invalid:5100", "gateway-2.invalid:5100", "gateway-3.invalid:5100"},
			Cadence:         5 * time.Second,
		},
		Thresholds: ThresholdsConfig{
			MinimumDataFilesystemBytes: formalFilesystemBytes, DiskSafeStopFreePercent: 5,
			Correctness: CorrectnessThresholds{OverallFirstAttemptFailure: FailureRateLimit{MaxFailures: 1, PerAttempts: 10_000, Operator: ComparisonLessThan}, AnyMinuteFirstAttemptFailure: FailureRateLimit{MaxFailures: 1, PerAttempts: 1_000, Operator: ComparisonLessOrEqual}},
			Latency:     LatencyThresholds{HotSendACK: LatencyLimit{P99: 200 * time.Millisecond, P999: time.Second}, Cold: LatencyLimit{P99: 2 * time.Second, P999: 5 * time.Second}, Sync: LatencyLimit{P99: time.Second, P999: 3 * time.Second}, SingleAnomaly: 10 * time.Second, SustainedBreachWindow: 5 * time.Minute},
			Resource:    ResourceThresholds{ForcedGCLiveHeapGrowthPercent: 5, ForcedGCLiveHeapWindow: 6 * time.Hour, GoroutineGrowthPercent: 5, GoroutineGrowthWindow: 24 * time.Hour},
			Cluster:     ClusterThresholds{HealthPollEvery: 5 * time.Second, UnhealthyFailAfter: 30 * time.Second, MaxHotReplicaLagEntries: 0, LeaderImbalancePercent: 20, LeaderImbalanceFor: 10 * time.Minute},
			Timeline:    TimelineThresholds{Warmup: 2 * time.Hour, Checkpoint: 24 * time.Hour, Final: formalCheckpointDuration},
		},
		Capacity: CapacityConfig{StartRatePerSecond: 2_000, RecoveryRatePerSecond: 2_000, StepPercent: 25, RefinePercent: 10, Step: CapacityStep{Stabilize: 10 * time.Minute, Measure: 20 * time.Minute}, RecoveryDuration: 30 * time.Minute},
	}
}
