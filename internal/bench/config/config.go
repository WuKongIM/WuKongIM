package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"gopkg.in/yaml.v3"
)

type Target = model.Target
type BenchAPIConfig = model.BenchAPIConfig
type MetricsConfig = model.MetricsConfig
type Worker = model.Worker
type WorkerSet = model.WorkerSet
type Scenario = model.Scenario
type RunConfig = model.RunConfig
type LimitsConfig = model.LimitsConfig
type IdentityConfig = model.IdentityConfig
type OnlineConfig = model.OnlineConfig
type ChannelProfilesConfig = model.ChannelProfilesConfig
type ChannelProfile = model.ChannelProfile
type MessageTrafficConfig = model.MessageTrafficConfig
type MessagePhase = model.MessagePhase

// LoadTarget reads a target YAML file, expands environment variables, and decodes it.
func LoadTarget(path string) (Target, error) {
	var target Target
	if err := loadYAMLFile(path, &target); err != nil {
		return Target{}, err
	}
	return target, nil
}

// LoadScenario reads a scenario YAML file, expands environment variables, and decodes it.
func LoadScenario(path string) (Scenario, error) {
	var scenario Scenario
	if err := loadYAMLFile(path, &scenario); err != nil {
		return Scenario{}, err
	}
	return scenario, nil
}

// LoadWorkerSet reads a worker YAML file, expands environment variables, and decodes it.
func LoadWorkerSet(path string) (WorkerSet, error) {
	var workers WorkerSet
	if err := loadYAMLFile(path, &workers); err != nil {
		return WorkerSet{}, err
	}
	return workers, nil
}

// ValidateTargetScenario checks target/scenario fields required before any run planning.
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
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
