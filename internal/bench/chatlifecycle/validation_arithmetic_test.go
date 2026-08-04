package chatlifecycle

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGenericWorkloadRelationships(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"worker count", func(c *Config) { c.Workload.Workers = 2 }, "workload.workers: must equal observation worker count"},
		{"negative group category", func(c *Config) { c.Workload.Groups.Large = -1 }, "workload.groups.large: must be in 0..2000"},
		{"group category over bound", func(c *Config) { c.Workload.Groups.Small = 2_001 }, "workload.groups.small: must be in 0..2000"},
		{"empty group catalog", func(c *Config) {
			c.Workload.Groups.Small = 0
			c.Workload.Groups.Medium = 0
			c.Workload.Groups.VeryLarge = 0
			c.Workload.Groups.VeryLargeMembers = 0
			c.Workload.Groups.VeryLargeSendEvery = 0
		}, "workload.groups: catalog total must be in 1..2000"},
		{"group total over bound", func(c *Config) { c.Workload.Groups.Small = 2_000 }, "workload.groups: catalog total must be in 1..2000"},
		{"group total differs from hot set", func(c *Config) { c.Workload.HotSet.GroupChannels-- }, "workload.hot_set.group_channels: must equal group catalog total"},
		{"membership is not fixed", func(c *Config) { c.Workload.Groups.FixedMembership = false }, "workload.groups.fixed_membership: must be true"},
		{"very-large members missing", func(c *Config) { c.Workload.Groups.VeryLargeMembers = 0 }, "workload.groups.very_large_members: must be greater than zero when very_large is positive"},
		{"very-large cadence missing", func(c *Config) { c.Workload.Groups.VeryLargeSendEvery = 0 }, "workload.groups.very_large_send_every: must be greater than zero when very_large is positive"},
		{"members without very-large group", func(c *Config) {
			c.Workload.Groups.Small++
			c.Workload.Groups.VeryLarge = 0
		}, "workload.groups.very_large_members: must be zero when very_large is zero"},
		{"cadence without very-large group", func(c *Config) {
			c.Workload.Groups.Small++
			c.Workload.Groups.VeryLarge = 0
			c.Workload.Groups.VeryLargeMembers = 0
		}, "workload.groups.very_large_send_every: must be zero when very_large is zero"},
		{"hot set exceeds channel bound", func(c *Config) { c.Workload.MaxChannelsPerNode = 99 }, "workload.max_channels_per_node: must cover active person and group hot-set channels"},
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

func TestBurstCreditAcceptsExactFractionalMessageCounts(t *testing.T) {
	tests := []struct {
		name       string
		credit     time.Duration
		rate       int
		maximum    int
		baseConfig func() Config
	}{
		{"formal reviewed value", 2 * time.Second, 2_000, 4_000, FormalConfig},
		{"local reviewed value", 2 * time.Second, 100, 200, LocalConfig},
		{"half second", 500 * time.Millisecond, 100, 50, LocalConfig},
		{"exact nanosecond fraction", 512 * time.Nanosecond, 1_953_125, 1, LocalConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.baseConfig()
			cfg.Workload.BurstCredit = tt.credit
			cfg.Workload.SendRatePerSecond = tt.rate
			cfg.Workload.MaxGlobalBurst = tt.maximum
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestBurstCreditArithmeticIsBounded(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{
			name: "non-integral fractional message count",
			mutate: func(c *Config) {
				c.Workload.BurstCredit = 500 * time.Millisecond
				c.Workload.SendRatePerSecond = 99
				c.Workload.MaxGlobalBurst = 49
			},
			want: "workload.max_global_burst: burst calculation must produce an integral message count",
		},
		{
			name: "quotient exceeds uint64",
			mutate: func(c *Config) {
				c.Workload.BurstCredit = time.Duration(math.MaxInt64)
				c.Workload.SendRatePerSecond = math.MaxInt
				c.Workload.MaxGlobalBurst = math.MaxInt
			},
			want: "workload.max_global_burst: burst calculation exceeds supported range",
		},
		{
			name: "quotient exceeds int",
			mutate: func(c *Config) {
				c.Workload.BurstCredit = 2 * time.Second
				c.Workload.SendRatePerSecond = math.MaxInt/2 + 1
				c.Workload.MaxGlobalBurst = math.MaxInt
			},
			want: "workload.max_global_burst: burst calculation exceeds supported range",
		},
		{
			name:   "nonpositive credit",
			mutate: func(c *Config) { c.Workload.BurstCredit = 0 },
			want:   "workload.burst_credit: must be greater than zero",
		},
		{
			name:   "nonpositive maximum",
			mutate: func(c *Config) { c.Workload.MaxGlobalBurst = 0 },
			want:   "workload.max_global_burst: must be greater than zero",
		},
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

func TestBurstCreditRejectsWrappedNanosecondProduct(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("wrapped positive admission requires a 64-bit int")
	}
	cfg := LocalConfig()
	cfg.Workload.BurstCredit = 2*time.Second + time.Nanosecond
	cfg.Workload.SendRatePerSecond = math.MaxInt
	// A signed 64-bit nanosecond multiplication wraps to this positive value
	// before division, even though the exact quotient exceeds uint64.
	wrappedMaximum := int64(9_223_372_034)
	cfg.Workload.MaxGlobalBurst = int(wrappedMaximum)
	want := "workload.max_global_burst: burst calculation exceeds supported range"
	if err := cfg.Validate(); err == nil || err.Error() != want {
		t.Fatalf("Validate() error = %v, want %q", err, want)
	}
}

func TestGroupCatalogArithmeticIsBounded(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GroupCatalogConfig)
		want   string
	}{
		{"small", func(g *GroupCatalogConfig) { g.Small = math.MaxInt }, "workload.groups.small: must be in 0..2000"},
		{"medium", func(g *GroupCatalogConfig) { g.Medium = math.MaxInt }, "workload.groups.medium: must be in 0..2000"},
		{"large", func(g *GroupCatalogConfig) { g.Large = math.MaxInt }, "workload.groups.large: must be in 0..2000"},
		{"very large", func(g *GroupCatalogConfig) { g.VeryLarge = math.MaxInt }, "workload.groups.very_large: must be in 0..2000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := LocalConfig()
			tt.mutate(&cfg.Workload.Groups)
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestHotSetArithmeticRejectsOverflow(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HotSetConfig)
	}{
		{"person channels", func(h *HotSetConfig) { h.PersonChannels = math.MaxInt }},
		{"group channels", func(h *HotSetConfig) { h.GroupChannels = math.MaxInt }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := LocalConfig()
			cfg.Workload.MaxChannelsPerNode = math.MaxInt
			tt.mutate(&cfg.Workload.HotSet)
			want := "workload.max_channels_per_node: must cover active person and group hot-set channels"
			if err := cfg.Validate(); err == nil || err.Error() != want {
				t.Fatalf("Validate() error = %v, want %q", err, want)
			}
		})
	}
}

func TestReviewedProfilesUseValidBoundedArithmetic(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"formal", FormalConfig()},
		{"local", LocalConfig()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creditSeconds := int(tt.cfg.Workload.BurstCredit / time.Second)
			if got, want := tt.cfg.Workload.MaxGlobalBurst, creditSeconds*tt.cfg.Workload.SendRatePerSecond; got != want {
				t.Fatalf("MaxGlobalBurst = %d, want %d", got, want)
			}
			groups := tt.cfg.Workload.Groups
			groupTotal := groups.Small + groups.Medium + groups.Large + groups.VeryLarge
			if groupTotal <= 0 || groupTotal > formalGroupCatalogTotal || groupTotal != tt.cfg.Workload.HotSet.GroupChannels {
				t.Fatalf("group total = %d, hot-set groups = %d", groupTotal, tt.cfg.Workload.HotSet.GroupChannels)
			}
			if err := tt.cfg.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestPercentageTotalsValidateSharesBeforeAddition(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"pair", func(c *Config) { c.Workload.Traffic.PersonPercent = math.MaxInt }, "workload.traffic: percentages must be in 0..100"},
		{"duration shares", func(c *Config) { c.Workload.Sessions[0].Percent = math.MaxInt }, "workload.sessions[0].percent: must be in 1..100"},
		{"lifecycle", func(c *Config) { c.Workload.Lifecycle.OneShot.Percent = math.MaxInt }, "workload.lifecycle.one_shot.percent: must be in 0..100"},
		{"payloads", func(c *Config) { c.Workload.Payloads[0].Percent = math.MaxInt }, "workload.payloads[0].percent: must be in 0..100"},
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

func TestGenericGroupCatalogAllowsReducedValues(t *testing.T) {
	cfg := LocalConfig()
	cfg.Workload.Groups = GroupCatalogConfig{Small: 17, Medium: 3, FixedMembership: true}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
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
			cfg := LocalConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.HasPrefix(err.Error(), tt.want+":") {
				t.Fatalf("Validate() error = %v, want %s field path", err, tt.want)
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
