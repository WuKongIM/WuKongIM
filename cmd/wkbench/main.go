package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/WuKongIM/WuKongIM/internal/bench/config"
	"github.com/WuKongIM/WuKongIM/internal/bench/coordinator"
	"github.com/WuKongIM/WuKongIM/internal/bench/planner"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
)

const (
	exitConfig    = 1
	exitPreflight = 2
	exitHardLimit = 3
	exitWorker    = 4
	exitTarget    = 5
	exitInternal  = 6
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	return runWithStderr(args, os.Stderr)
}

func runWithStderr(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: wkbench <run|worker|validate|doctor|report>")
		return exitConfig
	}
	switch args[0] {
	case "worker":
		return runWorker(args[1:], stderr)
	case "validate":
		return runValidate(args[1:], stderr)
	case "doctor":
		return runDoctor(args[1:], stderr)
	case "run":
		return runBench(args[1:], stderr)
	case "report":
		fmt.Fprintf(stderr, "%s is not implemented yet\n", args[0])
		return exitInternal
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return exitConfig
	}
}

func runBench(args []string, stderr io.Writer) int {
	targetCfg, scenario, workers, code := loadValidateInputs("run", args, stderr)
	if code != 0 {
		return code
	}
	coord := coordinator.New(coordinator.CoordinatorConfig{Workers: workers.Workers, Target: targetCfg})
	result, err := coord.Run(context.Background(), scenario)
	if err != nil {
		switch result.Status {
		case coordinator.StatusConfigFailed:
			fmt.Fprintf(stderr, "config validation failed: %v\n", err)
			return exitConfig
		case coordinator.StatusPreflightFailed:
			fmt.Fprintf(stderr, "preflight failed: %v\n", err)
			return exitPreflight
		case coordinator.StatusHardLimitFailed:
			fmt.Fprintf(stderr, "hard limit failed: %v\n", err)
			return exitHardLimit
		case coordinator.StatusWorkerFailed:
			fmt.Fprintf(stderr, "worker run failed: %v\n", err)
			return exitWorker
		case coordinator.StatusTargetUnavailable:
			fmt.Fprintf(stderr, "target unavailable: %v\n", err)
			return exitTarget
		default:
			fmt.Fprintf(stderr, "run failed: %v\n", err)
			return exitInternal
		}
	}
	fmt.Fprintln(stderr, "wkbench workload orchestration completed")
	return result.Status.ExitCode()
}

type benchConfigPaths struct {
	target   string
	scenario string
	workers  string
}

func runValidate(args []string, stderr io.Writer) int {
	targetCfg, scenario, workers, code := loadValidateInputs("validate", args, stderr)
	if code != 0 {
		return code
	}
	if _, err := planner.Build(scenario, workers.Workers); err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return exitConfig
	}
	_ = targetCfg
	return 0
}

func runDoctor(args []string, stderr io.Writer) int {
	targetCfg, workers, scenario, hasScenario, code := loadDoctorInputs("doctor", args, stderr)
	if code != 0 {
		return code
	}
	if hasScenario {
		if _, err := planner.Build(scenario, workers.Workers); err != nil {
			fmt.Fprintf(stderr, "config validation failed: %v\n", err)
			return exitConfig
		}
	}
	if err := coordinator.NewPreflight(coordinator.PreflightConfig{}).Check(context.Background(), targetCfg, workers); err != nil {
		fmt.Fprintf(stderr, "preflight failed: %v\n", err)
		return exitPreflight
	}
	return 0
}

func loadValidateInputs(name string, args []string, stderr io.Writer) (config.Target, config.Scenario, config.WorkerSet, int) {
	paths, code := parseBenchConfigPaths(name, args, stderr, true)
	if code != 0 {
		return config.Target{}, config.Scenario{}, config.WorkerSet{}, code
	}
	targetCfg, workers, code := loadTargetAndWorkers(paths, stderr)
	if code != 0 {
		return config.Target{}, config.Scenario{}, config.WorkerSet{}, code
	}
	scenario, err := config.LoadScenario(paths.scenario)
	if err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.Scenario{}, config.WorkerSet{}, exitConfig
	}
	if err := config.ValidateTargetScenario(targetCfg, scenario); err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.Scenario{}, config.WorkerSet{}, exitConfig
	}
	return targetCfg, scenario, workers, 0
}

func loadDoctorInputs(name string, args []string, stderr io.Writer) (config.Target, config.WorkerSet, config.Scenario, bool, int) {
	paths, code := parseBenchConfigPaths(name, args, stderr, false)
	if code != 0 {
		return config.Target{}, config.WorkerSet{}, config.Scenario{}, false, code
	}
	targetCfg, workers, code := loadTargetAndWorkers(paths, stderr)
	if code != 0 {
		return config.Target{}, config.WorkerSet{}, config.Scenario{}, false, code
	}
	if paths.scenario == "" {
		return targetCfg, workers, config.Scenario{}, false, 0
	}
	scenario, err := config.LoadScenario(paths.scenario)
	if err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.WorkerSet{}, config.Scenario{}, false, exitConfig
	}
	if err := config.ValidateTargetScenario(targetCfg, scenario); err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.WorkerSet{}, config.Scenario{}, false, exitConfig
	}
	return targetCfg, workers, scenario, true, 0
}

func loadTargetAndWorkers(paths benchConfigPaths, stderr io.Writer) (config.Target, config.WorkerSet, int) {
	targetCfg, err := config.LoadTarget(paths.target)
	if err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.WorkerSet{}, exitConfig
	}
	workers, err := config.LoadWorkerSet(paths.workers)
	if err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.WorkerSet{}, exitConfig
	}
	if err := config.ValidateStaticConfig(targetCfg, workers); err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.WorkerSet{}, exitConfig
	}
	return targetCfg, workers, 0
}

func parseBenchConfigPaths(name string, args []string, stderr io.Writer, scenarioRequired bool) (benchConfigPaths, int) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var paths benchConfigPaths
	fs.StringVar(&paths.target, "target", "", "target YAML file")
	fs.StringVar(&paths.scenario, "scenario", "", "scenario YAML file")
	fs.StringVar(&paths.workers, "workers", "", "workers YAML file")
	if err := fs.Parse(args); err != nil {
		return benchConfigPaths{}, exitConfig
	}
	if paths.target == "" {
		fmt.Fprintln(stderr, "--target is required")
		return benchConfigPaths{}, exitConfig
	}
	if scenarioRequired && paths.scenario == "" {
		fmt.Fprintln(stderr, "--scenario is required")
		return benchConfigPaths{}, exitConfig
	}
	if paths.workers == "" {
		fmt.Fprintln(stderr, "--workers is required")
		return benchConfigPaths{}, exitConfig
	}
	return paths, 0
}

type workerCLIConfig struct {
	listen string
	server worker.Config
}

func runWorker(args []string, stderr io.Writer) int {
	cfg, code := parseWorkerConfig(args, stderr)
	if code != 0 {
		return code
	}
	if err := http.ListenAndServe(cfg.listen, worker.NewServer(cfg.server)); err != nil {
		fmt.Fprintf(stderr, "worker server failed: %v\n", err)
		return exitConfig
	}
	return 0
}

func parseWorkerConfig(args []string, stderr io.Writer) (workerCLIConfig, int) {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfg := workerCLIConfig{listen: "127.0.0.1:19090"}
	fs.StringVar(&cfg.listen, "listen", cfg.listen, "worker control listen address")
	fs.StringVar(&cfg.server.WorkDir, "work-dir", "", "directory for worker control state")
	fs.StringVar(&cfg.server.ControlToken, "control-token", os.Getenv("WK_BENCH_WORKER_TOKEN"), "bearer token for worker control API")
	fs.BoolVar(&cfg.server.InsecureControl, "insecure-control", false, "allow unauthenticated worker control API")
	if err := fs.Parse(args); err != nil {
		return workerCLIConfig{}, exitConfig
	}
	if cfg.server.InsecureControl {
		cfg.server.ControlToken = ""
		return cfg, 0
	}
	if cfg.server.ControlToken == "" {
		fmt.Fprintln(stderr, "--control-token is required unless --insecure-control=true")
		return workerCLIConfig{}, exitConfig
	}
	return cfg, 0
}
