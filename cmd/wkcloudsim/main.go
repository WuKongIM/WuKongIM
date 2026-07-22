package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/app"
	benchconfig "github.com/WuKongIM/WuKongIM/internal/bench/config"
	cloudsimalibaba "github.com/WuKongIM/WuKongIM/internal/infra/cloudsim/alibaba"
	cloudsim "github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/spf13/cobra"
)

const (
	maxCreateRequestBytes  = 64 << 10
	maxProviderConfigBytes = 128 << 10
)

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr, time.Now))
}

func execute(args []string, stdout, stderr io.Writer, now func() time.Time) int {
	cmd := newRootCommand(stdout, stderr, now)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func newRootCommand(stdout, stderr io.Writer, now func() time.Time) *cobra.Command {
	var statePath string
	var providerName string
	var providerConfigPath string
	root := &cobra.Command{
		Use:           "wkcloudsim",
		Short:         "Provider-neutral WuKongIM cloud simulation lifecycle control",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().StringVar(&providerName, "provider", "fake", "cloud provider: fake or alibaba")
	root.PersistentFlags().StringVar(&statePath, "state", "", "persistent fake-provider inventory path")
	root.PersistentFlags().StringVar(&providerConfigPath, "provider-config", "", "strict provider configuration JSON path")
	control := func() (*cloudsim.ControlPlane, error) {
		switch providerName {
		case "fake":
			if strings.TrimSpace(statePath) == "" {
				return nil, errors.New("--state is required for the fake provider")
			}
			return app.NewFakeCloudSimulationControlPlane(statePath, now)
		case cloudsimalibaba.ProviderName:
			config, err := readAlibabaConfig(providerConfigPath)
			if err != nil {
				return nil, err
			}
			api, err := cloudsimalibaba.NewOpenAPIFromEnvironment(config.Region)
			if err != nil {
				return nil, err
			}
			provider, err := cloudsimalibaba.New(config, api, now)
			if err != nil {
				return nil, err
			}
			return cloudsim.NewControlPlane(provider, now), nil
		default:
			return nil, fmt.Errorf("unsupported --provider %q", providerName)
		}
	}
	root.AddCommand(
		newScenarioDigestCommand(stdout),
		newDiscoverConfigCommand(stdout, &providerName, func(region string) (cloudsimalibaba.ConfigDiscoveryAPI, error) {
			return cloudsimalibaba.NewOpenAPIFromEnvironment(region)
		}),
		newCreateCommand(stdout, control),
		newInventoryCommand(stdout, control),
		newStatusCommand(stdout, control),
		newTransitionCommand(stdout, control),
		newPreflightCommand(stdout, control),
		newOpenDeploymentCommand(stdout, control),
		newCloseDeploymentCommand(stdout, control),
		newOpenAnalysisCommand(stdout, control),
		newCloseAnalysisCommand(stdout, control),
		newOpenPublicViewCommand(stdout, control),
		newClosePublicViewCommand(stdout, control),
		newDestroyCommand(stdout, control),
		newSweepCommand(stdout, control),
	)
	return root
}

type configDiscoveryFactory func(string) (cloudsimalibaba.ConfigDiscoveryAPI, error)

func newDiscoverConfigCommand(stdout io.Writer, providerName *string, factory configDiscoveryFactory) *cobra.Command {
	var region string
	command := &cobra.Command{
		Use:   "discover-config",
		Short: "Discover a non-secret Alibaba provider config from live inventory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if providerName == nil || *providerName != cloudsimalibaba.ProviderName {
				return errors.New("discover-config requires --provider alibaba")
			}
			api, err := factory(region)
			if err != nil {
				return err
			}
			config, err := cloudsimalibaba.DiscoverConfig(cmd.Context(), api, region)
			if err != nil {
				return err
			}
			return writeJSON(stdout, config)
		},
	}
	command.Flags().StringVar(&region, "region", "", "Alibaba region used for live inventory discovery")
	_ = command.MarkFlagRequired("region")
	return command
}

func newScenarioDigestCommand(stdout io.Writer) *cobra.Command {
	var scenarioPath string
	cmd := &cobra.Command{
		Use:   "scenario-digest",
		Short: "Print the canonical digest of one effective wkbench/v1 scenario",
		RunE: func(*cobra.Command, []string) error {
			scenario, err := benchconfig.LoadScenario(scenarioPath)
			if err != nil {
				return err
			}
			digest, err := model.DigestScenario(scenario)
			if err != nil {
				return err
			}
			return writeJSON(stdout, map[string]string{"digest": digest})
		},
	}
	cmd.Flags().StringVar(&scenarioPath, "scenario", "", "effective wkbench/v1 scenario path")
	_ = cmd.MarkFlagRequired("scenario")
	return cmd
}

type controlFactory func() (*cloudsim.ControlPlane, error)

func newCreateCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	var requestPath string
	var locatorPath string
	var workflowRunID int64
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Validate guardrails and create one cloud Simulation Run",
		RunE: func(cmd *cobra.Command, _ []string) error {
			request, err := readCreateRequest(requestPath)
			if err != nil {
				return err
			}
			control, err := factory()
			if err != nil {
				return err
			}
			run, err := control.Create(cmd.Context(), request)
			if err != nil {
				return err
			}
			if locatorPath != "" {
				if workflowRunID <= 0 {
					return errors.New("--workflow-run-id is required with --locator")
				}
				locator := cloudsim.RunLocator{
					Schema: cloudsim.RunLocatorSchemaV1, RunID: run.ID, Provider: run.Provider, Region: run.Region,
					AccountIDHash: run.AccountIDHash, Repository: run.Repository, SourceSHA: request.SourceSHA,
					ScenarioDigest: request.ScenarioDigest, CreatedAt: run.CreatedAt, ExpiresAt: run.ExpiresAt,
					ProvisionWorkflowRunID: workflowRunID,
				}
				if err := writeRunLocator(locatorPath, locator); err != nil {
					_, _ = control.Destroy(context.Background(), run.ID)
					return fmt.Errorf("write locator and rolled back run: %w", err)
				}
			}
			return writeJSON(stdout, run)
		},
	}
	cmd.Flags().StringVar(&requestPath, "request", "", "strict CreateRequest JSON path")
	cmd.Flags().StringVar(&locatorPath, "locator", "", "optional minimal Run Locator output path")
	cmd.Flags().Int64Var(&workflowRunID, "workflow-run-id", 0, "provisioning GitHub workflow run ID")
	_ = cmd.MarkFlagRequired("request")
	return cmd
}

func newOpenDeploymentCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	return newOpenWindowCommand("open-deployment RUN_ID", "Open one bounded IPv4 /32 simulator SSH window", stdout, factory,
		func(ctx context.Context, control *cloudsim.ControlPlane, runID string, prefix netip.Prefix, until time.Time) (cloudsim.Run, error) {
			return control.OpenDeployment(ctx, cloudsim.OpenDeploymentRequest{RunID: runID, SourcePrefix: prefix, Until: until})
		})
}

func newCloseDeploymentCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	return lifecycleRunCommand("close-deployment RUN_ID", "Close temporary simulator SSH ingress", stdout, factory,
		func(ctx context.Context, control *cloudsim.ControlPlane, runID string) (cloudsim.Run, error) {
			return control.CloseDeployment(ctx, runID)
		})
}

func newStatusCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status RUN_ID",
		Short: "Reconcile one exact run from provider inventory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			control, err := factory()
			if err != nil {
				return err
			}
			run, err := control.Status(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return writeJSON(stdout, run)
		},
	}
	return cmd
}

func newInventoryCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "inventory",
		Short: "List authority-bound run candidates without proving release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			control, err := factory()
			if err != nil {
				return err
			}
			snapshot, err := control.InventorySnapshot(cmd.Context())
			if err != nil {
				return err
			}
			return writeJSON(stdout, snapshot)
		},
	}
}

func newTransitionCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	var activeUntilRaw string
	cmd := &cobra.Command{
		Use:   "transition RUN_ID STATE",
		Short: "Persist one allowed forward Simulation Run lifecycle step",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			request := cloudsim.TransitionRequest{RunID: args[0], Next: cloudsim.State(args[1])}
			if strings.TrimSpace(activeUntilRaw) != "" {
				activeUntil, err := time.Parse(time.RFC3339, activeUntilRaw)
				if err != nil {
					return fmt.Errorf("invalid --active-until: %w", err)
				}
				request.ActiveUntil = activeUntil
			}
			control, err := factory()
			if err != nil {
				return err
			}
			run, err := control.Transition(cmd.Context(), request)
			if err != nil {
				return err
			}
			return writeJSON(stdout, run)
		},
	}
	cmd.Flags().StringVar(&activeUntilRaw, "active-until", "", "RFC3339 workload deadline required for running")
	return cmd
}

func newPreflightCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	var locatorPath string
	cmd := &cobra.Command{
		Use: "preflight", Short: "Bind one exact Run Locator to current provider inventory", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			file, err := os.Open(locatorPath)
			if err != nil {
				return err
			}
			locator, decodeErr := cloudsim.DecodeRunLocator(file)
			closeErr := file.Close()
			if decodeErr != nil || closeErr != nil {
				return errors.Join(decodeErr, closeErr)
			}
			control, err := factory()
			if err != nil {
				return err
			}
			result, err := control.Preflight(cmd.Context(), locator)
			if err != nil {
				return err
			}
			return writeJSON(stdout, result)
		},
	}
	cmd.Flags().StringVar(&locatorPath, "locator", "", "strict minimal Run Locator path")
	_ = cmd.MarkFlagRequired("locator")
	return cmd
}

func newOpenAnalysisCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	return newOpenWindowCommand("open-analysis RUN_ID", "Open one bounded IPv4 /32 Analysis Access Window", stdout, factory,
		func(ctx context.Context, control *cloudsim.ControlPlane, runID string, prefix netip.Prefix, until time.Time) (cloudsim.Run, error) {
			return control.OpenAnalysis(ctx, cloudsim.OpenAnalysisRequest{RunID: runID, SourcePrefix: prefix, Until: until})
		})
}

func newOpenWindowCommand(use, short string, stdout io.Writer, factory controlFactory, operation func(context.Context, *cloudsim.ControlPlane, string, netip.Prefix, time.Time) (cloudsim.Run, error)) *cobra.Command {
	var source string
	var untilRaw string
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix, err := netip.ParsePrefix(source)
			if err != nil {
				return fmt.Errorf("invalid --source: %w", err)
			}
			until, err := time.Parse(time.RFC3339, untilRaw)
			if err != nil {
				return fmt.Errorf("invalid --until: %w", err)
			}
			control, err := factory()
			if err != nil {
				return err
			}
			run, err := operation(cmd.Context(), control, args[0], prefix, until)
			if err != nil {
				return err
			}
			return writeJSON(stdout, run)
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "admitted IPv4 prefix (/32 for runner access, 0.0.0.0/0 for Cloud View)")
	cmd.Flags().StringVar(&untilRaw, "until", "", "RFC3339 access-window expiry")
	_ = cmd.MarkFlagRequired("source")
	_ = cmd.MarkFlagRequired("until")
	return cmd
}

func newCloseAnalysisCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	return lifecycleRunCommand("close-analysis RUN_ID", "Close temporary Analysis MCP ingress", stdout, factory,
		func(ctx context.Context, control *cloudsim.ControlPlane, runID string) (cloudsim.Run, error) {
			return control.CloseAnalysis(ctx, runID)
		})
}

func newOpenPublicViewCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	return newOpenWindowCommand("open-public-view RUN_ID", "Open public Cloud View IPv4 ingress through the Run Lease", stdout, factory,
		func(ctx context.Context, control *cloudsim.ControlPlane, runID string, prefix netip.Prefix, until time.Time) (cloudsim.Run, error) {
			return control.OpenPublicView(ctx, cloudsim.OpenPublicViewRequest{RunID: runID, SourcePrefix: prefix, Until: until})
		})
}

func newClosePublicViewCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	return lifecycleRunCommand("close-public-view RUN_ID", "Close public Cloud View ingress", stdout, factory,
		func(ctx context.Context, control *cloudsim.ControlPlane, runID string) (cloudsim.Run, error) {
			return control.ClosePublicView(ctx, runID)
		})
}

func newDestroyCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	return lifecycleRunCommand("destroy RUN_ID", "Release all inventory for one exact run", stdout, factory,
		func(ctx context.Context, control *cloudsim.ControlPlane, runID string) (cloudsim.Run, error) {
			return control.Destroy(ctx, runID)
		})
}

func lifecycleRunCommand(use, short string, stdout io.Writer, factory controlFactory, operation func(context.Context, *cloudsim.ControlPlane, string) (cloudsim.Run, error)) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			control, err := factory()
			if err != nil {
				return err
			}
			run, err := operation(cmd.Context(), control, args[0])
			if err != nil {
				return err
			}
			return writeJSON(stdout, run)
		},
	}
}

func newSweepCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	return &cobra.Command{
		Use: "sweep", Short: "Destroy all expired unreleased fake-provider runs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			control, err := factory()
			if err != nil {
				return err
			}
			result, err := control.Sweep(cmd.Context())
			if writeErr := writeJSON(stdout, result); writeErr != nil {
				return writeErr
			}
			return err
		},
	}
}

func readCreateRequest(path string) (cloudsim.CreateRequest, error) {
	file, err := os.Open(path)
	if err != nil {
		return cloudsim.CreateRequest{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxCreateRequestBytes+1))
	decoder.DisallowUnknownFields()
	var request cloudsim.CreateRequest
	if err := decoder.Decode(&request); err != nil {
		return cloudsim.CreateRequest{}, fmt.Errorf("decode create request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return cloudsim.CreateRequest{}, errors.New("create request contains trailing data")
	}
	return request, nil
}

func readAlibabaConfig(path string) (cloudsimalibaba.Config, error) {
	if strings.TrimSpace(path) == "" {
		return cloudsimalibaba.Config{}, errors.New("--provider-config is required for the Alibaba provider")
	}
	file, err := os.Open(path)
	if err != nil {
		return cloudsimalibaba.Config{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxProviderConfigBytes+1))
	decoder.DisallowUnknownFields()
	var config cloudsimalibaba.Config
	if err := decoder.Decode(&config); err != nil {
		return cloudsimalibaba.Config{}, fmt.Errorf("decode Alibaba provider config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return cloudsimalibaba.Config{}, errors.New("Alibaba provider config contains trailing data")
	}
	return config, nil
}

func writeRunLocator(path string, locator cloudsim.RunLocator) error {
	var buf bytes.Buffer
	if err := cloudsim.EncodeRunLocator(&buf, locator); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".run-locator-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(buf.Bytes()); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
