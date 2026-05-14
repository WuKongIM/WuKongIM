package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"gopkg.in/yaml.v3"
)

type Target = model.Target
type TargetAPIConfig = model.TargetAPIConfig
type TargetGatewayConfig = model.TargetGatewayConfig
type TargetGatewayTCPConfig = model.TargetGatewayTCPConfig
type BenchAPIConfig = model.BenchAPIConfig
type MetricsConfig = model.MetricsConfig
type Worker = model.Worker
type WorkerSet = model.WorkerSet
type Scenario = model.Scenario
type RunConfig = model.RunConfig
type LimitsConfig = model.LimitsConfig
type HardLimitsConfig = model.HardLimitsConfig
type SoftLimitsConfig = model.SoftLimitsConfig
type PrepareConfig = model.PrepareConfig
type RetryConfig = model.RetryConfig
type IdentityConfig = model.IdentityConfig
type TokenConfig = model.TokenConfig
type OnlineConfig = model.OnlineConfig
type ReconnectConfig = model.ReconnectConfig
type HeartbeatConfig = model.HeartbeatConfig
type ChannelsConfig = model.ChannelsConfig
type ChannelProfile = model.ChannelProfile
type ParticipantsConfig = model.ParticipantsConfig
type MembersConfig = model.MembersConfig
type ChannelOnlineConfig = model.ChannelOnlineConfig
type ShardConfig = model.ShardConfig
type ChannelPrepareConfig = model.ChannelPrepareConfig
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
	var scenario Scenario
	if err := loadYAMLFile(path, &scenario); err != nil {
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

func loadYAMLFile(path string, out interface{}) error {
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
