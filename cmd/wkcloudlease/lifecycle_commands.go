package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
)

const (
	bootstrapAccessSchemaV1 = "wukongim.cloud_lease.bootstrap_access/v1"
	selectorSchemaV1        = "wukongim.cloud_lease.selector/v1"
	accessGrantSchemaV1     = "wukongim.cloud_lease.access_grant/v1"
	receiptSchemaV1         = "wukongim.cloud_lease.receipt/v1"
	releaseSchemaV1         = "wukongim.cloud_lease.release/v1"
	sweepSchemaV1           = "wukongim.cloud_lease.sweep/v1"
)

type bootstrapAccessDocument struct {
	Schema string                     `json:"schema"`
	Access cloudlease.BootstrapAccess `json:"access"`
}

type selectorDocument struct {
	Schema   string              `json:"schema"`
	Selector cloudlease.Selector `json:"selector"`
}

type accessGrantDocument struct {
	Schema string                 `json:"schema"`
	Grant  cloudlease.AccessGrant `json:"grant"`
}

type receiptResult struct {
	Schema  string             `json:"schema"`
	Receipt cloudlease.Receipt `json:"receipt"`
}

type releaseResult struct {
	Schema string                   `json:"schema"`
	Result cloudlease.ReleaseResult `json:"result"`
}

type sweepResult struct {
	Schema string                 `json:"schema"`
	Result cloudlease.SweepResult `json:"result"`
}

func addLifecycleCommands(root *cobra.Command, dependencies commandDependencies) {
	var acquirePlanPath, acquireQuotePath, bootstrapPath string
	acquire := &cobra.Command{
		Use:   "acquire",
		Short: "Create or reconstruct one explicitly authorized paid Cloud Lease",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runAcquire(command.Context(), command.OutOrStdout(), acquirePlanPath, acquireQuotePath, bootstrapPath, dependencies)
		},
	}
	acquire.Flags().StringVar(&acquirePlanPath, "plan", "", "path to the exact Cloud Lease Plan JSON")
	acquire.Flags().StringVar(&acquireQuotePath, "quote", "", "path to the versioned Quote JSON")
	acquire.Flags().StringVar(&bootstrapPath, "bootstrap-access", "", "path to the versioned public bootstrap access JSON")
	mustMarkRequired(acquire, "plan", "quote", "bootstrap-access")
	root.AddCommand(acquire)

	var inspectSelectorPath string
	inspect := &cobra.Command{
		Use: "inspect", Short: "Reconstruct one exact Cloud Lease from provider inventory", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runInspect(command.Context(), command.OutOrStdout(), inspectSelectorPath, dependencies)
		},
	}
	inspect.Flags().StringVar(&inspectSelectorPath, "selector", "", "path to the versioned exact Lease Selector JSON")
	mustMarkRequired(inspect, "selector")
	root.AddCommand(inspect)

	var grantSelectorPath, grantPath string
	grantAccess := &cobra.Command{
		Use: "grant_access", Short: "Create one exact expiring ingress grant for a live Lease", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runGrantAccess(command.Context(), command.OutOrStdout(), grantSelectorPath, grantPath, dependencies)
		},
	}
	grantAccess.Flags().StringVar(&grantSelectorPath, "selector", "", "path to the versioned exact Lease Selector JSON")
	grantAccess.Flags().StringVar(&grantPath, "grant", "", "path to the versioned exact Access Grant JSON")
	mustMarkRequired(grantAccess, "selector", "grant")
	root.AddCommand(grantAccess)

	var revokeSelectorPath, revokeGrantID string
	revokeAccess := &cobra.Command{
		Use: "revoke_access", Short: "Remove one exact ingress grant from a Lease", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runRevokeAccess(command.Context(), command.OutOrStdout(), revokeSelectorPath, revokeGrantID, dependencies)
		},
	}
	revokeAccess.Flags().StringVar(&revokeSelectorPath, "selector", "", "path to the versioned exact Lease Selector JSON")
	revokeAccess.Flags().StringVar(&revokeGrantID, "grant-id", "", "exact Access Grant identity")
	mustMarkRequired(revokeAccess, "selector", "grant-id")
	root.AddCommand(revokeAccess)

	var releaseSelectorPath string
	release := &cobra.Command{
		Use: "release", Short: "Delete one exact Cloud Lease and prove zero inventory", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runRelease(command.Context(), command.OutOrStdout(), releaseSelectorPath, dependencies)
		},
	}
	release.Flags().StringVar(&releaseSelectorPath, "selector", "", "path to the versioned exact Lease Selector JSON")
	mustMarkRequired(release, "selector")
	root.AddCommand(release)

	var sweepProvider, sweepRegion, sweepRepository string
	sweep := &cobra.Command{
		Use: "sweep", Short: "Reconcile expired repository Leases until released or pending", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runSweep(command.Context(), command.OutOrStdout(), sweepProvider, sweepRegion, sweepRepository, dependencies)
		},
	}
	sweep.Flags().StringVar(&sweepProvider, "provider", "", "exact provider identity")
	sweep.Flags().StringVar(&sweepRegion, "region", "", "exact provider region")
	sweep.Flags().StringVar(&sweepRepository, "repository", "", "exact owner/name inventory boundary")
	mustMarkRequired(sweep, "provider", "region", "repository")
	root.AddCommand(sweep)
}

func mustMarkRequired(command *cobra.Command, names ...string) {
	for _, name := range names {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
}

func runAcquire(ctx context.Context, stdout io.Writer, planPath, quotePath, bootstrapPath string, dependencies commandDependencies) error {
	plan, err := readPlan(planPath)
	if err != nil {
		return err
	}
	var quoted quoteResult
	if err := readStrictDocument(quotePath, &quoted); err != nil {
		return fmt.Errorf("read quote: %w", err)
	}
	if quoted.Schema != quoteSchemaV1 {
		return errors.New("quote schema is not supported")
	}
	var bootstrap bootstrapAccessDocument
	if err := readStrictDocument(bootstrapPath, &bootstrap); err != nil {
		return fmt.Errorf("read bootstrap access: %w", err)
	}
	if bootstrap.Schema != bootstrapAccessSchemaV1 {
		return errors.New("bootstrap access schema is not supported")
	}
	provider, err := constructLifecycleProvider(dependencies, plan.Provider, plan.Region)
	if err != nil {
		return err
	}
	receipt, err := cloudlease.NewController(provider, dependencies.now).AcquireWithBootstrap(ctx, plan, quoted.Quote, bootstrap.Access)
	if encodeErr := json.NewEncoder(stdout).Encode(receiptResult{Schema: receiptSchemaV1, Receipt: receipt}); encodeErr != nil {
		return encodeErr
	}
	if err != nil {
		return fmt.Errorf("acquire: %w", err)
	}
	return nil
}

func runInspect(ctx context.Context, stdout io.Writer, selectorPath string, dependencies commandDependencies) error {
	selector, err := readSelector(selectorPath)
	if err != nil {
		return err
	}
	provider, err := constructInventoryProvider(dependencies, selector.Provider, selector.Region)
	if err != nil {
		return err
	}
	receipt, err := cloudlease.NewController(provider, dependencies.now).Inspect(ctx, selector)
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}
	return json.NewEncoder(stdout).Encode(receiptResult{Schema: receiptSchemaV1, Receipt: receipt})
}

func runGrantAccess(ctx context.Context, stdout io.Writer, selectorPath, grantPath string, dependencies commandDependencies) error {
	selector, err := readSelector(selectorPath)
	if err != nil {
		return err
	}
	var document accessGrantDocument
	if err := readStrictDocument(grantPath, &document); err != nil {
		return fmt.Errorf("read access grant: %w", err)
	}
	if document.Schema != accessGrantSchemaV1 {
		return errors.New("access grant schema is not supported")
	}
	provider, err := constructLifecycleProvider(dependencies, selector.Provider, selector.Region)
	if err != nil {
		return err
	}
	receipt, err := cloudlease.NewController(provider, dependencies.now).GrantAccess(ctx, selector, document.Grant)
	if err != nil {
		return fmt.Errorf("grant access: %w", err)
	}
	return json.NewEncoder(stdout).Encode(receiptResult{Schema: receiptSchemaV1, Receipt: receipt})
}

func runRevokeAccess(ctx context.Context, stdout io.Writer, selectorPath, grantID string, dependencies commandDependencies) error {
	selector, err := readSelector(selectorPath)
	if err != nil {
		return err
	}
	provider, err := constructLifecycleProvider(dependencies, selector.Provider, selector.Region)
	if err != nil {
		return err
	}
	receipt, err := cloudlease.NewController(provider, dependencies.now).RevokeAccess(ctx, selector, grantID)
	if err != nil {
		return fmt.Errorf("revoke access: %w", err)
	}
	return json.NewEncoder(stdout).Encode(receiptResult{Schema: receiptSchemaV1, Receipt: receipt})
}

func runRelease(ctx context.Context, stdout io.Writer, selectorPath string, dependencies commandDependencies) error {
	selector, err := readSelector(selectorPath)
	if err != nil {
		return err
	}
	provider, err := constructLifecycleProvider(dependencies, selector.Provider, selector.Region)
	if err != nil {
		return err
	}
	result, err := cloudlease.NewController(provider, dependencies.now).Release(ctx, selector)
	if encodeErr := json.NewEncoder(stdout).Encode(releaseResult{Schema: releaseSchemaV1, Result: result}); encodeErr != nil {
		return encodeErr
	}
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	return nil
}

func runSweep(ctx context.Context, stdout io.Writer, providerName, region, repository string, dependencies commandDependencies) error {
	if strings.TrimSpace(providerName) != providerName || strings.TrimSpace(region) != region ||
		strings.TrimSpace(repository) != repository || providerName == "" || region == "" || repository == "" {
		return errors.New("provider, region, and repository must be exact non-empty values")
	}
	provider, err := constructLifecycleProvider(dependencies, providerName, region)
	if err != nil {
		return err
	}
	result, err := cloudlease.NewController(provider, dependencies.now).Sweep(ctx, cloudlease.SweepRequest{Repository: repository})
	if encodeErr := json.NewEncoder(stdout).Encode(sweepResult{Schema: sweepSchemaV1, Result: result}); encodeErr != nil {
		return encodeErr
	}
	if err != nil {
		return fmt.Errorf("sweep: %w", err)
	}
	return nil
}

func constructLifecycleProvider(dependencies commandDependencies, provider, region string) (cloudlease.Provider, error) {
	if dependencies.lifecycleProvider == nil {
		return nil, errors.New("lifecycle provider factory is unavailable")
	}
	result, err := dependencies.lifecycleProvider(provider, region)
	if err != nil {
		return nil, fmt.Errorf("construct lifecycle provider: %w", err)
	}
	return result, nil
}

func constructInventoryProvider(dependencies commandDependencies, provider, region string) (cloudlease.Provider, error) {
	if dependencies.inventoryProvider == nil {
		return nil, errors.New("inventory provider factory is unavailable")
	}
	result, err := dependencies.inventoryProvider(provider, region)
	if err != nil {
		return nil, fmt.Errorf("construct inventory provider: %w", err)
	}
	return result, nil
}

func readSelector(path string) (cloudlease.Selector, error) {
	var document selectorDocument
	if err := readStrictDocument(path, &document); err != nil {
		return cloudlease.Selector{}, fmt.Errorf("read selector: %w", err)
	}
	if document.Schema != selectorSchemaV1 {
		return cloudlease.Selector{}, errors.New("selector schema is not supported")
	}
	return document.Selector, nil
}

func readStrictDocument(path string, output any) error {
	if path == "" {
		return errors.New("document path is required")
	}
	var reader io.Reader
	var file *os.File
	if path == "-" {
		reader = os.Stdin
	} else {
		opened, err := os.Open(path)
		if err != nil {
			return err
		}
		file = opened
		defer file.Close()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxPlanBytes+1))
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maxPlanBytes {
		return errors.New("document must be non-empty and at most 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
