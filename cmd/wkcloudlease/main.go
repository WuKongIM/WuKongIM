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
	"time"

	"github.com/spf13/cobra"

	"github.com/WuKongIM/WuKongIM/internal/infra/cloudlease/alibaba"
	"github.com/WuKongIM/WuKongIM/internal/infra/cloudlease/fake"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

const (
	dryRunSchemaV1 = "wukongim.cloud_lease.dry_run/v1"
	quoteSchemaV1  = "wukongim.cloud_lease.quote/v1"
	maxPlanBytes   = 1 << 20
)

type dryRunResult struct {
	Schema            string   `json:"schema"`
	Provider          string   `json:"provider"`
	LeaseID           string   `json:"lease_id"`
	PlanDigest        string   `json:"plan_digest"`
	EstimatedCost     int64    `json:"estimated_cost_micros"`
	Operations        []string `json:"operations"`
	FinalState        string   `json:"final_state"`
	ResidualResources int      `json:"residual_resources"`
	SweepExamined     int      `json:"sweep_examined"`
}

type quoteResult struct {
	Schema string           `json:"schema"`
	Quote  cloudlease.Quote `json:"quote"`
}

type commandDependencies struct {
	now               func() time.Time
	quoteProvider     func(cloudlease.Plan) (cloudlease.Provider, error)
	inventoryProvider func(provider, region string) (cloudlease.Provider, error)
	lifecycleProvider func(provider, region string) (cloudlease.Provider, error)
}

func main() {
	command := newRootCommand(os.Stdout)
	command.SetErr(os.Stderr)
	if err := command.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand(stdout io.Writer) *cobra.Command {
	dependencies := commandDependencies{now: time.Now}
	dependencies.quoteProvider = func(plan cloudlease.Plan) (cloudlease.Provider, error) {
		switch plan.Provider {
		case alibaba.ProviderName:
			api, err := alibaba.NewOpenAPIFromOIDCEnvironment(plan.Region)
			if err != nil {
				return nil, err
			}
			return alibaba.New(api, alibaba.Options{Now: dependencies.now}), nil
		default:
			return nil, fmt.Errorf("unsupported quote provider %q", plan.Provider)
		}
	}
	dependencies.lifecycleProvider = func(provider, region string) (cloudlease.Provider, error) {
		switch provider {
		case alibaba.ProviderName:
			api, err := alibaba.NewLifecycleOpenAPIFromOIDCEnvironment(region)
			if err != nil {
				return nil, err
			}
			return alibaba.NewLifecycle(api, api, alibaba.Options{Now: dependencies.now}), nil
		default:
			return nil, fmt.Errorf("unsupported lifecycle provider %q", provider)
		}
	}
	dependencies.inventoryProvider = func(provider, region string) (cloudlease.Provider, error) {
		switch provider {
		case alibaba.ProviderName:
			api, err := alibaba.NewInventoryOpenAPIFromOIDCEnvironment(region)
			if err != nil {
				return nil, err
			}
			return alibaba.NewLifecycle(api, api, alibaba.Options{Now: dependencies.now}), nil
		default:
			return nil, fmt.Errorf("unsupported inventory provider %q", provider)
		}
	}
	return newRootCommandWithDependencies(stdout, dependencies)
}

func newRootCommandWithDependencies(stdout io.Writer, dependencies commandDependencies) *cobra.Command {
	if dependencies.now == nil {
		dependencies.now = time.Now
	}
	root := &cobra.Command{
		Use:           "wkcloudlease",
		Short:         "Operate provider-neutral temporary Cloud Leases",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.AddCommand(&cobra.Command{
		Use:   "dry-run",
		Short: "Exercise the complete lifecycle against an in-memory fake provider",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runDryRun(command.Context(), command.OutOrStdout())
		},
	})
	var planPath string
	quoteCommand := &cobra.Command{
		Use:   "quote",
		Short: "Discover a read-only provider Quote for one strict Cloud Lease Plan",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runQuote(command.Context(), command.OutOrStdout(), planPath, dependencies)
		},
	}
	quoteCommand.Flags().StringVar(&planPath, "plan", "", "path to a strict Cloud Lease Plan JSON file, or - for stdin")
	if err := quoteCommand.MarkFlagRequired("plan"); err != nil {
		panic(err)
	}
	root.AddCommand(quoteCommand)
	addLifecycleCommands(root, dependencies)
	return root
}

func runQuote(ctx context.Context, stdout io.Writer, planPath string, dependencies commandDependencies) error {
	plan, err := readPlan(planPath)
	if err != nil {
		return err
	}
	if dependencies.quoteProvider == nil {
		return errors.New("quote provider factory is unavailable")
	}
	provider, err := dependencies.quoteProvider(plan)
	if err != nil {
		return fmt.Errorf("construct quote provider: %w", err)
	}
	quote, err := cloudlease.NewController(provider, dependencies.now).Quote(ctx, plan)
	if err != nil {
		return fmt.Errorf("quote: %w", err)
	}
	return json.NewEncoder(stdout).Encode(quoteResult{Schema: quoteSchemaV1, Quote: quote})
}

func readPlan(path string) (cloudlease.Plan, error) {
	var reader io.Reader
	var file *os.File
	switch path {
	case "":
		return cloudlease.Plan{}, errors.New("plan path is required")
	case "-":
		reader = os.Stdin
	default:
		opened, err := os.Open(path)
		if err != nil {
			return cloudlease.Plan{}, fmt.Errorf("open plan: %w", err)
		}
		file = opened
		defer file.Close()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxPlanBytes+1))
	if err != nil {
		return cloudlease.Plan{}, fmt.Errorf("read plan: %w", err)
	}
	if len(data) == 0 || len(data) > maxPlanBytes {
		return cloudlease.Plan{}, errors.New("plan must be non-empty and at most 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var plan cloudlease.Plan
	if err := decoder.Decode(&plan); err != nil {
		return cloudlease.Plan{}, fmt.Errorf("decode plan: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return cloudlease.Plan{}, fmt.Errorf("decode plan: %w", err)
	}
	return plan, nil
}

func runDryRun(ctx context.Context, stdout io.Writer) error {
	now := time.Now().UTC()
	provider := fake.New(fake.Options{
		Now: func() time.Time { return now }, EstimatedCostMicros: 4_000_000,
	})
	controller := cloudlease.NewController(provider, func() time.Time { return now })
	plan := dryRunPlan(now)
	operations := make([]string, 0, 7)

	quote, err := controller.Quote(ctx, plan)
	if err != nil {
		return fmt.Errorf("quote: %w", err)
	}
	operations = append(operations, "quote")
	if _, err := controller.Acquire(ctx, plan, quote); err != nil {
		return fmt.Errorf("acquire: %w", err)
	}
	operations = append(operations, "acquire")
	selector := cloudlease.Selector{
		LeaseID: plan.LeaseID, RequestID: plan.RequestID, Provider: plan.Provider,
		Region: plan.Region, Repository: plan.Repository, PlanDigest: quote.PlanDigest,
	}
	if _, err := controller.Inspect(ctx, selector); err != nil {
		return fmt.Errorf("inspect: %w", err)
	}
	operations = append(operations, "inspect")
	grant := cloudlease.AccessGrant{
		ID: "ssh", TargetRole: "host", Protocol: cloudlease.ProtocolTCP,
		PortFrom: 22, PortTo: 22, SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"),
		Until: plan.ExpiresAt,
	}
	if _, err := controller.GrantAccess(ctx, selector, grant); err != nil {
		return fmt.Errorf("grant access: %w", err)
	}
	operations = append(operations, "grant_access")
	if _, err := controller.RevokeAccess(ctx, selector, grant.ID); err != nil {
		return fmt.Errorf("revoke access: %w", err)
	}
	operations = append(operations, "revoke_access")
	released, err := controller.Release(ctx, selector)
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	operations = append(operations, "release")
	sweep, err := controller.Sweep(ctx, cloudlease.SweepRequest{Repository: plan.Repository})
	if err != nil {
		return fmt.Errorf("sweep: %w", err)
	}
	operations = append(operations, "sweep")

	return json.NewEncoder(stdout).Encode(dryRunResult{
		Schema: dryRunSchemaV1, Provider: provider.Name(), LeaseID: released.ZeroInventory.Selector.LeaseID,
		PlanDigest: quote.PlanDigest, EstimatedCost: quote.EstimatedCostMicros,
		Operations: operations, FinalState: string(cloudlease.StateReleased),
		ResidualResources: 0, SweepExamined: sweep.Examined,
	})
}

func dryRunPlan(now time.Time) cloudlease.Plan {
	expiresAt := now.Add(time.Hour)
	return cloudlease.Plan{
		Schema: cloudlease.PlanSchemaV1, LeaseID: "dry-run-lease", RequestID: "dry-run-request",
		Provider: fake.ProviderName, Region: "fake-region", Repository: "WuKongIM/WuKongIM",
		Operator: "dry-run", ExpiresAt: expiresAt,
		Budget: cloudlease.Budget{Currency: "CNY", LimitMicros: 10_000_000, CommittedMicros: 1_000_000},
		Network: cloudlease.NetworkPlan{
			Isolated: true, SingleZone: true,
			InitialAccess: []cloudlease.AccessGrant{{
				ID: "http", TargetRole: "host", Protocol: cloudlease.ProtocolTCP,
				PortFrom: 80, PortTo: 80, SourcePrefix: netip.MustParsePrefix("0.0.0.0/0"),
				Until: expiresAt,
			}},
		},
		HostGroups: []cloudlease.HostGroupPlan{{
			Role: "host", Count: 1,
			Compute: cloudlease.ComputePlan{
				VCPUs: 1, MemoryBytes: 1 << 30, Architecture: "x86_64", BillingModel: "fake",
			},
			SystemDisk: cloudlease.DiskPlan{Role: "system", SizeBytes: 10 << 30, Class: "fake"},
			PublicIPv4: true, InternetEgress: true, PeakBandwidthMbps: 1,
		}},
		Tags: map[string]string{"purpose": "dry-run"},
	}
}
