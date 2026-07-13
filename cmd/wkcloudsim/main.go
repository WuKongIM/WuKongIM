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
	cloudsim "github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/spf13/cobra"
)

const maxCreateRequestBytes = 64 << 10

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
	root := &cobra.Command{
		Use:           "wkcloudsim",
		Short:         "Provider-neutral WuKongIM cloud simulation lifecycle control",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().StringVar(&statePath, "state", "", "persistent fake-provider inventory path")
	control := func() (*cloudsim.ControlPlane, error) {
		if strings.TrimSpace(statePath) == "" {
			return nil, errors.New("--state is required for the Phase 1 fake provider")
		}
		return app.NewFakeCloudSimulationControlPlane(statePath, now)
	}
	root.AddCommand(
		newScenarioDigestCommand(stdout),
		newCreateCommand(stdout, control),
		newStatusCommand(stdout, control),
		newOpenAnalysisCommand(stdout, control),
		newCloseAnalysisCommand(stdout, control),
		newDestroyCommand(stdout, control),
		newSweepCommand(stdout, control),
	)
	return root
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
		Short: "Validate guardrails and create one fake-provider Simulation Run",
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

func newStatusCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status RUN_ID",
		Short: "Reconcile one exact run from fake provider inventory",
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

func newOpenAnalysisCommand(stdout io.Writer, factory controlFactory) *cobra.Command {
	var source string
	var untilRaw string
	cmd := &cobra.Command{
		Use:   "open-analysis RUN_ID",
		Short: "Open one bounded IPv4 /32 Analysis Access Window",
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
			run, err := control.OpenAnalysis(cmd.Context(), cloudsim.OpenAnalysisRequest{RunID: args[0], SourcePrefix: prefix, Until: until})
			if err != nil {
				return err
			}
			return writeJSON(stdout, run)
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "GitHub runner public IPv4 /32")
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
