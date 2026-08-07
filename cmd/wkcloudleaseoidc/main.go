// Command wkcloudleaseoidc bootstraps and verifies workflow-conditioned Alibaba identities.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/WuKongIM/WuKongIM/internal/infra/cloudlease/alibaba"
)

const (
	maxIdentityConfigBytes = 128 << 10
	identityConfigSchemaV1 = "wukongim.cloud_lease.oidc_bootstrap_config/v1"
	identityPlanSchemaV1   = "wukongim.cloud_lease.oidc_bootstrap_plan/v1"
	identityResultSchemaV1 = "wukongim.cloud_lease.oidc_bootstrap/v1"
	identityVerifySchemaV1 = "wukongim.cloud_lease.oidc_identity/v1"
)

type identityConfigDocument struct {
	Schema    string                          `json:"schema"`
	Bootstrap alibaba.IdentityBootstrapConfig `json:"bootstrap"`
}

type identityPlanDocument struct {
	Schema string                        `json:"schema"`
	Plan   alibaba.IdentityBootstrapPlan `json:"plan"`
}

type identityResultDocument struct {
	Schema string                          `json:"schema"`
	Result alibaba.IdentityBootstrapResult `json:"result"`
}

type identityVerifyDocument struct {
	Schema        string `json:"schema"`
	Role          string `json:"role"`
	AccountIDHash string `json:"account_id_hash"`
}

type identityDependencies struct {
	fingerprints func(context.Context) ([]string, error)
	bootstrapAPI func(string) (alibaba.IdentityBootstrapAPI, error)
	verifyRole   func(context.Context, string, string, string, string, string, alibaba.IdentityPolicyKind) (string, error)
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}

func execute(args []string, stdout, stderr io.Writer) int {
	root := newRootCommand(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func newRootCommand(stdout io.Writer) *cobra.Command {
	dependencies := identityDependencies{
		fingerprints: alibaba.ResolveCloudLeaseGitHubOIDCFingerprints,
		bootstrapAPI: func(region string) (alibaba.IdentityBootstrapAPI, error) {
			return alibaba.NewIdentityBootstrapOpenAPIFromAccessKeyEnvironment(region)
		},
		verifyRole: verifyLiveRole,
	}
	return newRootCommandWithDependencies(stdout, dependencies)
}

func newRootCommandWithDependencies(stdout io.Writer, dependencies identityDependencies) *cobra.Command {
	root := &cobra.Command{
		Use: "wkcloudleaseoidc", Short: "Bootstrap and verify WuKongIM Cloud Lease OIDC roles",
		SilenceUsage: true, SilenceErrors: true,
	}
	root.SetOut(stdout)
	for _, operation := range []string{"plan", "apply", "remove"} {
		operation := operation
		var configPath string
		command := &cobra.Command{
			Use: operation, Short: operation + " the repository-owned Alibaba OIDC identities", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return runIdentityOperation(cmd.Context(), stdout, operation, configPath, dependencies)
			},
		}
		command.Flags().StringVar(&configPath, "config", "", "strict non-secret OIDC bootstrap JSON path")
		if err := command.MarkFlagRequired("config"); err != nil {
			panic(err)
		}
		root.AddCommand(command)
	}
	var region, repository, providerARN, audience, role, policyKind string
	verify := &cobra.Command{
		Use: "verify", Short: "Verify the current temporary OIDC role and exact policy", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kind := alibaba.IdentityPolicyKind(policyKind)
			accountHash, err := dependencies.verifyRole(cmd.Context(), region, repository, providerARN, audience, role, kind)
			if err != nil {
				return err
			}
			return writeIdentityJSON(stdout, identityVerifyDocument{
				Schema: identityVerifySchemaV1, Role: role, AccountIDHash: accountHash,
			})
		},
	}
	verify.Flags().StringVar(&region, "region", "", "exact Alibaba region")
	verify.Flags().StringVar(&repository, "repository", "", "exact owner/name GitHub repository")
	verify.Flags().StringVar(&providerARN, "oidc-provider-arn", "", "exact Alibaba OIDC provider ARN")
	verify.Flags().StringVar(&audience, "audience", "", "exact OIDC audience")
	verify.Flags().StringVar(&role, "expected-role", "", "exact expected assumed role")
	verify.Flags().StringVar(&policyKind, "policy-kind", "", "provisioner, observer, or releaser")
	for _, flag := range []string{"region", "repository", "oidc-provider-arn", "audience", "expected-role", "policy-kind"} {
		if err := verify.MarkFlagRequired(flag); err != nil {
			panic(err)
		}
	}
	root.AddCommand(verify)
	return root
}

func runIdentityOperation(ctx context.Context, stdout io.Writer, operation, configPath string, dependencies identityDependencies) error {
	document, err := readIdentityConfig(configPath)
	if err != nil {
		return err
	}
	config := document.Bootstrap
	if len(config.OIDCFingerprints) == 0 {
		config.OIDCFingerprints, err = dependencies.fingerprints(ctx)
		if err != nil {
			return err
		}
	}
	api, err := dependencies.bootstrapAPI(config.Region)
	if err != nil {
		return err
	}
	bootstrapper, err := alibaba.NewIdentityBootstrapper(config, api)
	if err != nil {
		return err
	}
	switch operation {
	case "plan":
		plan, err := bootstrapper.Plan(ctx)
		if err != nil {
			return err
		}
		return writeIdentityJSON(stdout, identityPlanDocument{Schema: identityPlanSchemaV1, Plan: plan})
	case "apply":
		result, err := bootstrapper.Apply(ctx)
		if err != nil {
			return err
		}
		return writeIdentityJSON(stdout, identityResultDocument{Schema: identityResultSchemaV1, Result: result})
	case "remove":
		result, err := bootstrapper.Remove(ctx)
		if err != nil {
			return err
		}
		return writeIdentityJSON(stdout, identityResultDocument{Schema: identityResultSchemaV1, Result: result})
	default:
		return errors.New("unsupported identity operation")
	}
}

func verifyLiveRole(ctx context.Context, region, repository, providerARN, audience, role string, kind alibaba.IdentityPolicyKind) (string, error) {
	if region != alibaba.RegionHangzhou || strings.TrimSpace(role) == "" {
		return "", alibaba.ErrIdentityBootstrapConfig
	}
	document, err := alibaba.IdentityRolePolicyDocument(kind)
	if err != nil {
		return "", err
	}
	api, err := alibaba.NewOpenAPIFromOIDCEnvironment(region)
	if err != nil {
		return "", err
	}
	if err := api.AssertCallerRole(ctx, role); err != nil {
		return "", err
	}
	if err := api.AssertExactRolePolicy(ctx, role, role, document); err != nil {
		return "", err
	}
	trust, err := alibaba.ExpectedIdentityRoleTrust(repository, "main", providerARN, audience, role)
	if err != nil {
		return "", err
	}
	if err := api.AssertExactRoleTrust(ctx, role, trust); err != nil {
		return "", err
	}
	return api.AccountIDHash(ctx)
}

func readIdentityConfig(path string) (identityConfigDocument, error) {
	file, err := os.Open(path)
	if err != nil {
		return identityConfigDocument{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxIdentityConfigBytes+1))
	decoder.DisallowUnknownFields()
	var document identityConfigDocument
	if err := decoder.Decode(&document); err != nil {
		return identityConfigDocument{}, fmt.Errorf("decode identity config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return identityConfigDocument{}, errors.New("identity config contains trailing data")
	}
	if document.Schema != identityConfigSchemaV1 {
		return identityConfigDocument{}, errors.New("unsupported identity config schema")
	}
	return document, nil
}

func writeIdentityJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
