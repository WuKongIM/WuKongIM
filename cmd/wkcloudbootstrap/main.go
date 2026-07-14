// Command wkcloudbootstrap performs the one-time Alibaba CloudShell identity bootstrap.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/WuKongIM/WuKongIM/internal/infra/cloudsim/alibaba"
)

const maxBootstrapConfigBytes = 128 << 10

type commandConfig struct {
	Bootstrap alibaba.BootstrapConfig `json:"bootstrap"`
	Provider  alibaba.Config          `json:"provider"`
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}

func execute(args []string, stdout, stderr io.Writer) int {
	root := newRootCommand(stdout, stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	var configPath string
	root := &cobra.Command{
		Use: "wkcloudbootstrap", Short: "Bootstrap WuKongIM GitHub OIDC roles from Alibaba CloudShell",
		SilenceUsage: true, SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().StringVar(&configPath, "config", "", "strict non-secret bootstrap JSON path")
	_ = root.MarkPersistentFlagRequired("config")
	for _, operation := range []string{"plan", "apply", "remove"} {
		operation := operation
		root.AddCommand(&cobra.Command{
			Use: operation, Short: operation + " the repository-owned OIDC roles and policies", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				config, err := readCommandConfig(configPath)
				if err != nil {
					return err
				}
				return runOperation(cmd.Context(), stdout, operation, config)
			},
		})
	}
	return root
}

func runOperation(ctx context.Context, stdout io.Writer, operation string, config commandConfig) error {
	if len(config.Bootstrap.OIDCFingerprints) == 0 {
		fingerprints, err := alibaba.ResolveGitHubOIDCFingerprints(ctx)
		if err != nil {
			return err
		}
		config.Bootstrap.OIDCFingerprints = fingerprints
	}
	digest := sha256.Sum256([]byte(config.Bootstrap.AccountID))
	config.Provider.AccountIDHash = "sha256:" + hex.EncodeToString(digest[:])
	if config.Provider.Region == "" {
		config.Provider.Region = config.Bootstrap.Region
	}
	cloudAPI, err := alibaba.NewOpenAPIFromDefaultCredential(config.Bootstrap.Region)
	if err != nil {
		return err
	}
	provider, err := alibaba.New(config.Provider, cloudAPI, time.Now)
	if err != nil {
		return err
	}
	bootstrapAPI, err := alibaba.NewCloudShellBootstrapAPI(config.Bootstrap.Region, provider)
	if err != nil {
		return err
	}
	bootstrap, err := alibaba.NewBootstrapper(config.Bootstrap, bootstrapAPI, time.Now)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	switch operation {
	case "plan":
		result, err := bootstrap.Plan(ctx)
		if err != nil {
			return err
		}
		return encoder.Encode(result)
	case "apply":
		result, err := bootstrap.Apply(ctx)
		if err != nil {
			return err
		}
		return encoder.Encode(result)
	case "remove":
		result, err := bootstrap.Remove(ctx)
		if err != nil {
			return err
		}
		return encoder.Encode(result)
	default:
		return errors.New("unsupported bootstrap operation")
	}
}

func readCommandConfig(path string) (commandConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return commandConfig{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxBootstrapConfigBytes+1))
	decoder.DisallowUnknownFields()
	var config commandConfig
	if err := decoder.Decode(&config); err != nil {
		return commandConfig{}, fmt.Errorf("decode bootstrap config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return commandConfig{}, errors.New("bootstrap config contains trailing data")
	}
	return config, nil
}
