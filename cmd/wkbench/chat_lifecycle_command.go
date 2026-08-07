package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	exitChatLifecycleProduct        = 7
	exitChatLifecycleHarness        = 8
	exitChatLifecycleInfrastructure = 9
	exitChatLifecycleOperatorStop   = 130
)

type chatLifecycleCLIConfig struct {
	configPath     string
	checkpointPath string
	outputDir      string
	config         chatlifecycle.Config
	checkpoint     chatlifecycle.Report
}

func newSoakChatLifecycleCommand(stderr io.Writer) *cobra.Command {
	var cli chatLifecycleCLIConfig
	cmd := &cobra.Command{
		Use:   "chat-lifecycle",
		Short: "Run continuous chat catalog hot, cold, and reheat validation",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := loadSoakChatLifecycleConfig(&cli); err != nil {
				return exitConfigError(err)
			}
			return exitCodeError(runChatLifecycleCLI(cli, stderr))
		},
	}
	bindChatLifecycleFlags(cmd.Flags(), &cli, false)
	return cmd
}

func newCapacityChatLifecycleCommand(stderr io.Writer) *cobra.Command {
	var cli chatLifecycleCLIConfig
	cmd := &cobra.Command{
		Use:   "chat-lifecycle",
		Short: "Search capacity on the same live 72-hour aged dataset",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := loadCapacityChatLifecycleConfig(&cli); err != nil {
				return exitConfigError(err)
			}
			return exitCodeError(runChatLifecycleCLI(cli, stderr))
		},
	}
	bindChatLifecycleFlags(cmd.Flags(), &cli, true)
	return cmd
}

func bindChatLifecycleFlags(flags *pflag.FlagSet, cli *chatLifecycleCLIConfig, capacity bool) {
	flags.StringVar(&cli.configPath, "config", "", "strict chat-lifecycle YAML file")
	if capacity {
		flags.StringVar(&cli.checkpointPath, "checkpoint", "", "completed passing 72-hour Soak JSON checkpoint")
	}
	flags.StringVar(&cli.outputDir, "output-dir", "", "checkpoint and final report output directory")
}

func validateChatLifecyclePaths(cli chatLifecycleCLIConfig, capacity bool) error {
	if strings.TrimSpace(cli.configPath) == "" {
		return fmt.Errorf("--config is required")
	}
	if capacity && strings.TrimSpace(cli.checkpointPath) == "" {
		return fmt.Errorf("--checkpoint is required")
	}
	if strings.TrimSpace(cli.outputDir) == "" {
		return fmt.Errorf("--output-dir is required")
	}
	return nil
}

func loadSoakChatLifecycleConfig(cli *chatLifecycleCLIConfig) error {
	if cli == nil {
		return fmt.Errorf("chat-lifecycle command configuration is required")
	}
	if err := validateChatLifecyclePaths(*cli, false); err != nil {
		return err
	}
	cfg, err := chatlifecycle.LoadConfig(cli.configPath)
	if err != nil {
		return err
	}
	if cfg.Mode != chatlifecycle.ModeSoak {
		return fmt.Errorf("chat-lifecycle config mode must be soak")
	}
	cli.config = cfg
	return nil
}

func loadCapacityChatLifecycleConfig(cli *chatLifecycleCLIConfig) error {
	if cli == nil {
		return fmt.Errorf("chat-lifecycle command configuration is required")
	}
	if err := validateChatLifecyclePaths(*cli, true); err != nil {
		return err
	}
	report, err := chatlifecycle.ReadReport(cli.checkpointPath)
	if err != nil {
		return err
	}
	if report.Profile != chatlifecycle.ProfileFormal || report.Mode != chatlifecycle.ModeSoak || report.Stage != chatlifecycle.StageFormal ||
		report.Kind != chatlifecycle.CheckpointFinal || !report.Final || report.Continue ||
		!report.Continuous ||
		!report.Verdict.Terminal || report.Verdict.Outcome != chatlifecycle.VerdictPass ||
		report.Window.Elapsed < 72*time.Hour || report.Capacity.Attempted {
		return fmt.Errorf("checkpoint must be a completed passing 72-hour formal Soak report")
	}
	cfg, err := chatlifecycle.LoadConfig(cli.configPath)
	if err != nil {
		return err
	}
	if cfg.Mode != chatlifecycle.ModeCapacity {
		return fmt.Errorf("chat-lifecycle config mode must be capacity")
	}
	cli.config, cli.checkpoint = cfg, report
	return nil
}

func parseCapacityChatLifecycleConfig(args []string, stderr io.Writer) (chatLifecycleCLIConfig, int) {
	var cli chatLifecycleCLIConfig
	flags := pflag.NewFlagSet("capacity chat-lifecycle", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	bindChatLifecycleFlags(flags, &cli, true)
	if err := flags.Parse(args); err != nil {
		return chatLifecycleCLIConfig{}, exitConfig
	}
	if err := loadCapacityChatLifecycleConfig(&cli); err != nil {
		fmt.Fprintln(stderr, err)
		return chatLifecycleCLIConfig{}, exitConfig
	}
	return cli, 0
}

type chatLifecycleRunResult struct {
	Verdict chatlifecycle.VerdictSnapshot
	Summary string
}

type chatLifecycleCommandRunner interface {
	Run(context.Context) (chatLifecycleRunResult, error)
	RequestStop()
}

var newChatLifecycleCommandRunner = composeProductionChatLifecycleRunner

var runChatLifecycleCLI = func(cli chatLifecycleCLIConfig, stderr io.Writer) int {
	runner, err := newChatLifecycleCommandRunner(cli)
	if err != nil || runner == nil {
		fmt.Fprintln(stderr, "chat-lifecycle runner configuration failed")
		return exitInternal
	}
	return runPreparedChatLifecycleCLI(runner, stderr)
}

func runPreparedChatLifecycleCLI(runner chatLifecycleCommandRunner, stderr io.Writer) int {
	if runner == nil || stderr == nil {
		return exitInternal
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	return runChatLifecycleRunner(context.Background(), runner, signals, os.Exit, stderr)
}

func runChatLifecycleRunner(
	ctx context.Context,
	runner chatLifecycleCommandRunner,
	signals <-chan os.Signal,
	hardExit func(int),
	stderr io.Writer,
) int {
	if ctx == nil || runner == nil || hardExit == nil || stderr == nil {
		return exitInternal
	}
	type runCompletion struct {
		result chatLifecycleRunResult
		err    error
	}
	completed := make(chan runCompletion, 1)
	go func() {
		result, err := runner.Run(ctx)
		completed <- runCompletion{result: result, err: err}
	}()
	stopRequested := false
	contextDone := ctx.Done()
	for {
		select {
		case completion := <-completed:
			if completion.result.Summary != "" {
				fmt.Fprint(stderr, completion.result.Summary)
			}
			code := chatLifecycleVerdictExitCode(completion.result.Verdict)
			if completion.err != nil && code == 0 {
				return exitInternal
			}
			return code
		case received := <-signals:
			if !stopRequested {
				stopRequested = true
				runner.RequestStop()
				continue
			}
			code := chatLifecycleSignalExitCode(received)
			hardExit(code)
			return code
		case <-contextDone:
			if !stopRequested {
				stopRequested = true
				runner.RequestStop()
			}
			contextDone = nil
		}
	}
}

func chatLifecycleVerdictExitCode(verdict chatlifecycle.VerdictSnapshot) int {
	if !verdict.Terminal {
		return exitInternal
	}
	switch verdict.Outcome {
	case chatlifecycle.VerdictPass, chatlifecycle.VerdictRehearsalPass, chatlifecycle.VerdictPassedWithCapacityWarning:
		return 0
	case chatlifecycle.VerdictProductFailure:
		return exitChatLifecycleProduct
	case chatlifecycle.VerdictHarnessInvalid, chatlifecycle.VerdictInsufficientEvidence:
		return exitChatLifecycleHarness
	case chatlifecycle.VerdictInfrastructureFailure:
		return exitChatLifecycleInfrastructure
	case chatlifecycle.VerdictOperatorStop:
		return exitChatLifecycleOperatorStop
	default:
		return exitInternal
	}
}

func chatLifecycleSignalExitCode(received os.Signal) int {
	switch received {
	case os.Interrupt:
		return 130
	case syscall.SIGTERM:
		return 143
	default:
		return exitInternal
	}
}
