package model

import "time"

// Target describes the black-box WuKongIM deployment endpoints used by wkbench.
type Target struct {
	// Name is a human-readable target identifier used in reports.
	Name string `json:"name" yaml:"name"`
	// API contains HTTP API endpoints for normal client/server requests.
	API TargetAPIConfig `json:"api" yaml:"api"`
	// Gateway contains gateway endpoints used by load workers.
	Gateway TargetGatewayConfig `json:"gateway" yaml:"gateway"`
	// Metrics contains optional metrics scrape settings for reports and doctor checks.
	Metrics MetricsConfig `json:"metrics" yaml:"metrics"`
	// BenchAPI controls access to the benchmark-only server API surface.
	BenchAPI BenchAPIConfig `json:"bench_api" yaml:"bench_api"`
}

// TargetAPIConfig contains target HTTP API endpoints.
type TargetAPIConfig struct {
	// Addrs are HTTP API base addresses for normal client/server requests.
	Addrs []string `json:"addrs" yaml:"addrs"`
}

// TargetGatewayConfig contains target gateway endpoint groups.
type TargetGatewayConfig struct {
	// TCP contains TCP gateway endpoints.
	TCP TargetGatewayTCPConfig `json:"tcp" yaml:"tcp"`
}

// TargetGatewayTCPConfig contains TCP gateway endpoints.
type TargetGatewayTCPConfig struct {
	// Addrs are client gateway TCP addresses used by load workers.
	Addrs []string `json:"addrs" yaml:"addrs"`
}

// BenchAPIConfig configures the benchmark preparation HTTP API exposed by the target.
type BenchAPIConfig struct {
	// Enabled must be true because wkbench is a black-box client of the bench API.
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Addrs are optional explicit bench API base addresses; API.Addrs are used when empty.
	Addrs []string `json:"addrs" yaml:"addrs"`
	// Token is an optional shared token for protected benchmark preparation endpoints.
	Token string `json:"token" yaml:"token"`
}

// MetricsConfig describes optional target metrics endpoints.
type MetricsConfig struct {
	// Enabled indicates whether wkbench should collect target metrics.
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Addrs are metrics endpoint base addresses.
	Addrs []string `json:"addrs" yaml:"addrs"`
}

// Worker describes one wkbench worker process available to a run coordinator.
type Worker struct {
	// ID is the stable worker identifier used in run assignments.
	ID string `json:"id" yaml:"id"`
	// Addr is the worker control address.
	Addr string `json:"addr" yaml:"addr"`
	// Weight is the relative share of load assigned to this worker.
	Weight float64 `json:"weight" yaml:"weight"`
	// ControlToken is an optional shared token for worker control requests.
	ControlToken string `json:"control_token" yaml:"control_token"`
	// InsecureControl allows unauthenticated worker control checks when explicitly enabled.
	InsecureControl bool `json:"insecure_control" yaml:"insecure_control"`
	// Client optionally overrides per-session client queue and buffer capacities for this worker.
	Client *WorkerClientConfig `json:"client,omitempty" yaml:"client,omitempty"`
	// TCPSource optionally assigns a finite worker-local IPv4 source address and port pool.
	TCPSource *TCPSourceConfig `json:"tcp_source,omitempty" yaml:"tcp_source,omitempty"`
	// Tags are free-form labels for worker selection.
	Tags []string `json:"tags" yaml:"tags"`
}

// WorkerClientConfig contains worker-specific WKProto client capacity overrides.
// When Client is configured on a Worker, every field must be greater than zero.
type WorkerClientConfig struct {
	// SendQueueCapacity bounds each client's outbound SEND queue.
	SendQueueCapacity int `json:"send_queue_capacity" yaml:"send_queue_capacity"`
	// MaxInflight bounds each client's SEND operations waiting for SENDACK.
	MaxInflight int `json:"max_inflight" yaml:"max_inflight"`
	// ReadBufferSize is the socket reader scratch-buffer size in bytes.
	ReadBufferSize int `json:"read_buffer_size" yaml:"read_buffer_size"`
	// FrameBufferSize bounds both the bench adapter queue and the inner inbound RECV queue.
	FrameBufferSize int `json:"frame_buffer_size" yaml:"frame_buffer_size"`
}

// TCPSourceConfig defines the explicit local IPv4 address and port candidates
// available to one worker. Every candidate is reserved for at most one dial
// attempt during an assignment.
type TCPSourceConfig struct {
	// IPv4Addrs contains unique, configured local IPv4 addresses.
	IPv4Addrs []string `json:"ipv4_addrs" yaml:"ipv4_addrs"`
	// PortMin is the inclusive first local TCP port and must be at least 1024.
	PortMin int `json:"port_min" yaml:"port_min"`
	// PortMax is the inclusive last local TCP port and must not exceed 65535.
	PortMax int `json:"port_max" yaml:"port_max"`
}

// WorkerSet groups configured workers for a benchmark run.
type WorkerSet struct {
	// Workers is the list of available wkbench worker processes.
	Workers []Worker `json:"workers" yaml:"workers"`
}

// Scenario describes a wkbench/v1 load scenario.
type Scenario struct {
	// Version is the scenario schema version and must currently be "wkbench/v1".
	Version string `json:"version" yaml:"version"`
	// Run contains run identity and lifecycle controls.
	Run RunConfig `json:"run" yaml:"run"`
	// Limits contains hard and soft failure thresholds.
	Limits LimitsConfig `json:"limits" yaml:"limits"`
	// Prepare controls benchmark data preparation concurrency and retries.
	Prepare PrepareConfig `json:"prepare" yaml:"prepare"`
	// Identity describes generated users, devices, client message IDs, and token mode.
	Identity IdentityConfig `json:"identity" yaml:"identity"`
	// Online describes connection, balancing, reconnect, and heartbeat behavior.
	Online OnlineConfig `json:"online" yaml:"online"`
	// Channels contains channel profile definitions used by traffic phases.
	Channels ChannelsConfig `json:"channels" yaml:"channels"`
	// Cleanup controls whether benchmark data is removed after a run.
	Cleanup CleanupConfig `json:"cleanup" yaml:"cleanup"`
	// Messages describes payloads and message traffic generated by the scenario.
	Messages MessagesConfig `json:"messages" yaml:"messages"`
}

// RunConfig contains run identity and common execution controls.
type RunConfig struct {
	// ID is the stable benchmark run identifier.
	ID string `json:"id" yaml:"id"`
	// Duration is the active load duration.
	Duration time.Duration `json:"duration" yaml:"duration"`
	// Warmup is the period before measurements are considered steady-state.
	Warmup time.Duration `json:"warmup" yaml:"warmup"`
	// Cooldown is the period after active load for draining and final metrics.
	Cooldown time.Duration `json:"cooldown" yaml:"cooldown"`
	// RandomSeed makes generated scenario data reproducible when non-zero.
	RandomSeed int64 `json:"random_seed" yaml:"random_seed"`
	// FailFast stops the run after the first unrecoverable worker error.
	FailFast bool `json:"fail_fast" yaml:"fail_fast"`
	// ReportDir is the directory where reports should be written.
	ReportDir string `json:"report_dir" yaml:"report_dir"`
}

// LimitsConfig contains hard and soft thresholds for run failure decisions.
type LimitsConfig struct {
	// FailOnSoft promotes soft-limit violations to run failures when true.
	FailOnSoft bool `json:"fail_on_soft" yaml:"fail_on_soft"`
	// Hard contains thresholds that always fail the run when exceeded.
	Hard HardLimitsConfig `json:"hard" yaml:"hard"`
	// Soft contains thresholds that warn or fail depending on FailOnSoft.
	Soft SoftLimitsConfig `json:"soft" yaml:"soft"`
}

// HardLimitsConfig contains hard run failure thresholds.
type HardLimitsConfig struct {
	// MaxWorkerFailed is the maximum number of failed workers allowed.
	MaxWorkerFailed int `json:"max_worker_failed" yaml:"max_worker_failed"`
	// MaxConnectErrorRate is the maximum allowed gateway connect error rate.
	MaxConnectErrorRate float64 `json:"max_connect_error_rate" yaml:"max_connect_error_rate"`
	// MaxSendackErrorRate is the maximum allowed sendack error rate.
	MaxSendackErrorRate float64 `json:"max_sendack_error_rate" yaml:"max_sendack_error_rate"`
	// MaxRecvVerifyErrorRate is the maximum allowed receive verification error rate.
	MaxRecvVerifyErrorRate float64 `json:"max_recv_verify_error_rate" yaml:"max_recv_verify_error_rate"`
}

// SoftLimitsConfig contains soft run quality thresholds.
type SoftLimitsConfig struct {
	// MaxSendackP99 is the maximum desired sendack p99 latency.
	MaxSendackP99 time.Duration `json:"max_sendack_p99" yaml:"max_sendack_p99"`
	// MaxRecvP99 is the maximum desired receive p99 latency.
	MaxRecvP99 time.Duration `json:"max_recv_p99" yaml:"max_recv_p99"`
}

// PrepareConfig controls benchmark data preparation behavior.
type PrepareConfig struct {
	// Concurrency is the number of concurrent preparation operations.
	Concurrency int `json:"concurrency" yaml:"concurrency"`
	// RateLimit caps preparation request throughput.
	RateLimit Rate `json:"rate_limit" yaml:"rate_limit"`
	// Retry controls retries for preparation requests.
	Retry RetryConfig `json:"retry" yaml:"retry"`
}

// RetryConfig describes retry policy for preparation operations.
type RetryConfig struct {
	// MaxAttempts is the maximum number of attempts per operation.
	MaxAttempts int `json:"max_attempts" yaml:"max_attempts"`
	// Backoff is the base delay between retry attempts.
	Backoff time.Duration `json:"backoff" yaml:"backoff"`
}

// IdentityConfig describes generated benchmark identity names and token strategy.
type IdentityConfig struct {
	// UIDPrefix is prepended to generated user IDs.
	UIDPrefix string `json:"uid_prefix" yaml:"uid_prefix"`
	// DevicePrefix is prepended to generated device IDs.
	DevicePrefix string `json:"device_prefix" yaml:"device_prefix"`
	// ClientMsgPrefix is prepended to generated client message numbers.
	ClientMsgPrefix string `json:"client_msg_prefix" yaml:"client_msg_prefix"`
	// Token describes how worker clients obtain user tokens.
	Token TokenConfig `json:"token" yaml:"token"`
}

// TokenConfig describes user token source strategy.
type TokenConfig struct {
	// Mode selects the token source, for example bench_api.
	Mode string `json:"mode" yaml:"mode"`
}

// OnlineConfig describes simulated online-session behavior.
type OnlineConfig struct {
	// TotalUsers is the target number of users that should connect.
	TotalUsers int `json:"total_users" yaml:"total_users"`
	// ConnectRate controls how quickly workers open gateway connections.
	ConnectRate Rate `json:"connect_rate" yaml:"connect_rate"`
	// GatewayBalance selects how gateway addresses are assigned to workers.
	GatewayBalance string `json:"gateway_balance" yaml:"gateway_balance"`
	// Reconnect controls reconnect attempts after gateway disconnects.
	Reconnect ReconnectConfig `json:"reconnect" yaml:"reconnect"`
	// Heartbeat controls client heartbeat behavior.
	Heartbeat HeartbeatConfig `json:"heartbeat" yaml:"heartbeat"`
}

// ReconnectConfig describes client reconnect behavior.
type ReconnectConfig struct {
	// Enabled indicates whether workers should reconnect disconnected clients.
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Backoff is the base delay between reconnect attempts.
	Backoff time.Duration `json:"backoff" yaml:"backoff"`
	// MaxAttempts is the maximum reconnect attempts per client.
	MaxAttempts int `json:"max_attempts" yaml:"max_attempts"`
}

// HeartbeatConfig describes client heartbeat behavior.
type HeartbeatConfig struct {
	// Enabled indicates whether workers should send heartbeat frames.
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Interval is the delay between heartbeat frames.
	Interval time.Duration `json:"interval" yaml:"interval"`
	// Timeout is the maximum wait for heartbeat responses.
	Timeout time.Duration `json:"timeout" yaml:"timeout"`
}

// ChannelsConfig groups all channel profiles used by the scenario.
type ChannelsConfig struct {
	// Profiles are named channel shapes referenced by message traffic.
	Profiles []ChannelProfile `json:"profiles" yaml:"profiles"`
}

const (
	// ChannelTypePerson identifies generated one-to-one conversation channels.
	ChannelTypePerson = "person"
	// ChannelTypeGroup identifies generated group conversation channels.
	ChannelTypeGroup = "group"
	// ShardModeSplitMembersAndTraffic splits one logical group by member and traffic partitions.
	ShardModeSplitMembersAndTraffic = "split_members_and_traffic"
)

// ChannelProfile describes a class of channels prepared for a scenario.
type ChannelProfile struct {
	// Name is a stable profile name referenced by traffic phases.
	Name string `json:"name" yaml:"name"`
	// ChannelType is the target channel type, such as group or person.
	ChannelType string `json:"channel_type" yaml:"channel_type"`
	// Count is the number of channels created for this profile.
	Count int `json:"count" yaml:"count"`
	// Participants controls how participants are selected for this profile.
	Participants ParticipantsConfig `json:"participants" yaml:"participants"`
	// Members controls member set size, selection, and overlap.
	Members MembersConfig `json:"members" yaml:"members"`
	// Online controls online ratios for senders, recipients, and members.
	Online ChannelOnlineConfig `json:"online" yaml:"online"`
	// Shard controls channel distribution strategy.
	Shard ShardConfig `json:"shard" yaml:"shard"`
	// Prepare controls channel preparation details.
	Prepare ChannelPrepareConfig `json:"prepare" yaml:"prepare"`
}

// ParticipantsConfig describes participant selection for a channel profile.
type ParticipantsConfig struct {
	// Pick selects the participant selection strategy.
	Pick string `json:"pick" yaml:"pick"`
}

// MembersConfig describes member selection for a channel profile.
type MembersConfig struct {
	// Count is the target member count per channel.
	Count int `json:"count" yaml:"count"`
	// Pick selects the member selection strategy.
	Pick string `json:"pick" yaml:"pick"`
	// Overlap selects whether member reuse across generated channels is allowed.
	Overlap string `json:"overlap" yaml:"overlap"`
}

// ChannelOnlineConfig describes online ratios for a channel profile.
type ChannelOnlineConfig struct {
	// SenderRatio is the ratio of senders expected online.
	SenderRatio float64 `json:"sender_ratio" yaml:"sender_ratio"`
	// RecipientRatio is the ratio of recipients expected online.
	RecipientRatio float64 `json:"recipient_ratio" yaml:"recipient_ratio"`
	// MemberRatio is the ratio of members expected online.
	MemberRatio float64 `json:"member_ratio" yaml:"member_ratio"`
}

// ShardConfig describes channel distribution strategy.
type ShardConfig struct {
	// Mode selects the sharding strategy.
	Mode string `json:"mode" yaml:"mode"`
}

// ChannelPrepareConfig describes channel preparation details.
type ChannelPrepareConfig struct {
	// SubscribersBatchSize is the subscriber batch size used by preparation calls.
	SubscribersBatchSize int `json:"subscribers_batch_size" yaml:"subscribers_batch_size"`
}

// CleanupConfig describes benchmark data cleanup behavior.
type CleanupConfig struct {
	// Enabled indicates whether cleanup runs after the benchmark.
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Strategy selects what benchmark data cleanup should do.
	Strategy string `json:"strategy" yaml:"strategy"`
}

// MessagesConfig describes payloads and generated message traffic.
type MessagesConfig struct {
	// Payload describes generated message payloads.
	Payload PayloadConfig `json:"payload" yaml:"payload"`
	// Traffic contains named message traffic streams.
	Traffic []TrafficConfig `json:"traffic" yaml:"traffic"`
}

// PayloadConfig describes generated message payload shape.
type PayloadConfig struct {
	// SizeBytes is the generated message payload size in bytes.
	SizeBytes int `json:"size_bytes" yaml:"size_bytes"`
	// Mode selects the payload generation strategy.
	Mode string `json:"mode" yaml:"mode"`
}

// TrafficConfig describes one named message traffic stream.
type TrafficConfig struct {
	// Name is the traffic stream identifier used in reports.
	Name string `json:"name" yaml:"name"`
	// ChannelRef selects which channel profile receives this stream's messages.
	ChannelRef string `json:"channel_ref" yaml:"channel_ref"`
	// RatePerChannel is the per-channel send rate for this stream.
	RatePerChannel Rate `json:"rate_per_channel" yaml:"rate_per_channel"`
	// Concurrency is the maximum in-flight send operations for this traffic stream; zero preserves sequential sends.
	Concurrency int `json:"concurrency" yaml:"concurrency"`
	// AckTimeout bounds one sendack wait for this traffic stream; zero uses the worker default.
	AckTimeout time.Duration `json:"ack_timeout" yaml:"ack_timeout"`
	// RecvTimeout bounds one recv verification wait for this traffic stream; zero uses the worker default.
	RecvTimeout time.Duration `json:"recv_timeout" yaml:"recv_timeout"`
	// SenderPick selects how senders are chosen.
	SenderPick string `json:"sender_pick" yaml:"sender_pick"`
	// RecvAck controls whether simulated clients send receive acknowledgements.
	RecvAck bool `json:"recv_ack" yaml:"recv_ack"`
	// Verify controls receive verification sampling.
	Verify VerifyConfig `json:"verify" yaml:"verify"`
}

// VerifyConfig groups traffic verification settings.
type VerifyConfig struct {
	// Recv controls receive verification mode and sample size.
	Recv RecvVerifyConfig `json:"recv" yaml:"recv"`
}

// RecvVerifyConfig describes receive verification sampling.
type RecvVerifyConfig struct {
	// Mode selects receive verification strategy.
	Mode string `json:"mode" yaml:"mode"`
	// SampleSizePerMessage is the number of recipients sampled per message.
	SampleSizePerMessage int `json:"sample_size_per_message" yaml:"sample_size_per_message"`
}
