package config

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"gopkg.in/yaml.v3"
)

type Target = model.Target
type TargetAPIConfig = model.TargetAPIConfig
type TargetGatewayConfig = model.TargetGatewayConfig
type TargetGatewayTCPConfig = model.TargetGatewayTCPConfig
type BenchAPIConfig = model.BenchAPIConfig
type MetricsConfig = model.MetricsConfig
type Worker = model.Worker
type WorkerClientConfig = model.WorkerClientConfig
type TCPSourceConfig = model.TCPSourceConfig
type WorkerSet = model.WorkerSet
type Scenario = model.Scenario
type RunConfig = model.RunConfig
type ObjectivesConfig = model.ObjectivesConfig
type LimitsConfig = model.LimitsConfig
type HardLimitsConfig = model.HardLimitsConfig
type SoftLimitsConfig = model.SoftLimitsConfig
type PrepareConfig = model.PrepareConfig
type RetryConfig = model.RetryConfig
type IdentityConfig = model.IdentityConfig
type TokenConfig = model.TokenConfig
type OnlineConfig = model.OnlineConfig
type ReconnectConfig = model.ReconnectConfig
type ChurnConfig = model.ChurnConfig
type HeartbeatConfig = model.HeartbeatConfig
type ChannelsConfig = model.ChannelsConfig
type ChannelProfile = model.ChannelProfile
type ParticipantsConfig = model.ParticipantsConfig
type MembersConfig = model.MembersConfig
type ChannelOnlineConfig = model.ChannelOnlineConfig
type ShardConfig = model.ShardConfig
type ChannelPrepareConfig = model.ChannelPrepareConfig
type CleanupConfig = model.CleanupConfig
type MessagesConfig = model.MessagesConfig
type PayloadConfig = model.PayloadConfig
type TrafficConfig = model.TrafficConfig
type VerifyConfig = model.VerifyConfig
type RecvVerifyConfig = model.RecvVerifyConfig

// LoadTarget reads a target YAML file, expands environment variables, and decodes it strictly.
func LoadTarget(path string) (Target, error) {
	var target Target
	if err := loadYAMLFile(path, &target); err != nil {
		return Target{}, err
	}
	return target, nil
}

// LoadScenario reads a scenario YAML file, expands environment variables, and decodes it strictly.
func LoadScenario(path string) (Scenario, error) {
	scenario := Scenario{Limits: defaultLimitsConfig()}
	if err := loadYAMLFileInto(path, &scenario); err != nil {
		return Scenario{}, err
	}
	return scenario, nil
}

// LoadWorkerSet reads a worker YAML file, expands environment variables, and decodes it strictly.
func LoadWorkerSet(path string) (WorkerSet, error) {
	var workers WorkerSet
	if err := loadYAMLFile(path, &workers); err != nil {
		return WorkerSet{}, err
	}
	return workers, nil
}

// ValidateTargetScenario validates only early coordinator preflight fields.
func ValidateTargetScenario(target Target, scenario Scenario) error {
	var problems []string
	if !target.BenchAPI.Enabled {
		problems = append(problems, "target.bench_api.enabled must be true")
	}
	if scenario.Version != "wkbench/v1" {
		problems = append(problems, "scenario.version must be wkbench/v1")
	}
	if strings.TrimSpace(scenario.Run.ID) == "" {
		problems = append(problems, "scenario.run.id is required")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid wkbench config: %s", strings.Join(problems, "; "))
	}
	return nil
}

// ValidateStaticConfig validates deterministic target and worker fields before network preflight.
func ValidateStaticConfig(target Target, workers WorkerSet) error {
	var problems []string
	benchAddrs := nonEmptyStrings(target.BenchAPI.Addrs)
	apiAddrs := nonEmptyStrings(target.API.Addrs)
	if len(benchAddrs) == 0 && len(apiAddrs) == 0 {
		problems = append(problems, "target.api.addrs is required when target.bench_api.addrs is empty")
	}
	for idx, addr := range apiAddrs {
		if err := validateHTTPURL(addr); err != nil {
			problems = append(problems, fmt.Sprintf("target.api.addrs[%d] %v", idx, err))
		}
	}
	for idx, addr := range benchAddrs {
		if err := validateHTTPURL(addr); err != nil {
			problems = append(problems, fmt.Sprintf("target.bench_api.addrs[%d] %v", idx, err))
		}
	}
	if len(nonEmptyStrings(target.Gateway.TCP.Addrs)) == 0 {
		problems = append(problems, "target.gateway.tcp.addrs is required")
	}
	for idx, worker := range workers.Workers {
		prefix := fmt.Sprintf("workers[%d]", idx)
		if strings.TrimSpace(worker.ID) == "" {
			problems = append(problems, prefix+".id is required")
		}
		addr := strings.TrimSpace(worker.Addr)
		if addr == "" {
			problems = append(problems, prefix+".addr is required")
		} else if err := validateHTTPURL(addr); err != nil {
			problems = append(problems, fmt.Sprintf("%s.addr %v", prefix, err))
		}
		if worker.Weight <= 0 {
			problems = append(problems, prefix+".weight must be greater than zero")
		}
		if !worker.InsecureControl && strings.TrimSpace(worker.ControlToken) == "" {
			problems = append(problems, prefix+".control_token is required unless insecure_control=true")
		}
		if worker.Client != nil {
			if worker.Client.SendQueueCapacity <= 0 {
				problems = append(problems, prefix+".client.send_queue_capacity must be greater than zero")
			}
			if worker.Client.MaxInflight <= 0 {
				problems = append(problems, prefix+".client.max_inflight must be greater than zero")
			}
			if worker.Client.ReadBufferSize <= 0 {
				problems = append(problems, prefix+".client.read_buffer_size must be greater than zero")
			}
			if worker.Client.FrameBufferSize <= 0 {
				problems = append(problems, prefix+".client.frame_buffer_size must be greater than zero")
			}
		}
		if worker.TCPSource != nil {
			if err := model.ValidateTCPSourceConfig(worker.TCPSource); err != nil {
				problems = append(problems, fmt.Sprintf("%s.tcp_source.%v", prefix, err))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid wkbench config: %s", strings.Join(problems, "; "))
	}
	return nil
}

func nonEmptyStrings(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("must be an absolute http URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("must use http or https")
	}
	return nil
}

func loadYAMLFile(path string, out interface{}) error {
	return loadYAMLFileInto(path, out)
}

func loadYAMLFileInto(path string, out interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	data = []byte(os.ExpandEnv(string(data)))
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func defaultLimitsConfig() LimitsConfig {
	return LimitsConfig{Hard: HardLimitsConfig{MaxWorkerFailed: -1, MaxConnectErrorRate: -1, MaxSendackErrorRate: -1, MaxRecvVerifyErrorRate: -1}}
}
