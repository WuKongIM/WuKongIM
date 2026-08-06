package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/WuKongIM/WuKongIM/internal/infra/cloudlease/fake"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

const dryRunSchemaV1 = "wukongim.cloud_lease.dry_run/v1"

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

func main() {
	command := newRootCommand(os.Stdout)
	command.SetErr(os.Stderr)
	if err := command.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand(stdout io.Writer) *cobra.Command {
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
	return root
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
