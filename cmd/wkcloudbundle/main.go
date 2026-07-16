// Command wkcloudbundle renders and verifies immutable cloud deployment bundles.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	benchconfig "github.com/WuKongIM/WuKongIM/internal/bench/config"
	"github.com/WuKongIM/WuKongIM/internal/infra/cloudsim/deploy"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const maxBundleSpecBytes = 128 << 10

type bundleSpecFile struct {
	// RunID is the exact Simulation Run identity.
	RunID string `json:"run_id"`
	// SourceSHA is the trusted source revision.
	SourceSHA string `json:"source_sha"`
	// ScenarioPath is the repository-owned benchmark scenario.
	ScenarioPath string `json:"scenario_path"`
	// ScenarioDigest is the source scenario identity.
	ScenarioDigest string `json:"scenario_digest"`
	// Duration is the allowlisted active workload duration.
	Duration string `json:"duration"`
	// PrivateIPv4 maps every fixed host role to its private address.
	PrivateIPv4 map[string]string `json:"private_ipv4"`
	// SimulatorSourceIPv4 contains the simulator TCP source pool.
	SimulatorSourceIPv4 []string `json:"simulator_source_ipv4"`
	// PublicObservation requests the simulator-only public Cloud View payload.
	PublicObservation bool `json:"public_observation"`
}

func main() { os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr)) }

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
	root := &cobra.Command{Use: "wkcloudbundle", Short: "Render and verify cloud deployment bundles", SilenceUsage: true, SilenceErrors: true}
	root.SetOut(stdout)
	root.SetErr(stderr)
	var renderRoot, renderSpec string
	render := &cobra.Command{Use: "render", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		spec, err := readBundleSpec(renderSpec)
		if err != nil {
			return err
		}
		if err := deploy.Render(renderRoot, spec); err != nil {
			return err
		}
		effective, err := benchconfig.LoadScenario(filepath.Join(renderRoot, "config", "scenario.yaml"))
		if err != nil {
			return err
		}
		spec.ScenarioDigest, err = model.DigestScenario(effective)
		if err != nil {
			return err
		}
		manifest, err := deploy.Seal(renderRoot, spec)
		if err != nil {
			return err
		}
		return writeJSON(stdout, manifest)
	}}
	render.Flags().StringVar(&renderRoot, "root", "", "bundle root containing prebuilt static binaries")
	render.Flags().StringVar(&renderSpec, "spec", "", "strict non-secret bundle spec JSON")
	_ = render.MarkFlagRequired("root")
	_ = render.MarkFlagRequired("spec")
	var verifyRoot string
	verify := &cobra.Command{Use: "verify", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		manifest, err := deploy.Verify(verifyRoot)
		if err != nil {
			return err
		}
		return writeJSON(stdout, manifest)
	}}
	verify.Flags().StringVar(&verifyRoot, "root", "", "bundle root")
	_ = verify.MarkFlagRequired("root")
	root.AddCommand(render, verify)
	return root
}

func readBundleSpec(path string) (deploy.BundleSpec, error) {
	file, err := os.Open(path)
	if err != nil {
		return deploy.BundleSpec{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxBundleSpecBytes+1))
	decoder.DisallowUnknownFields()
	var raw bundleSpecFile
	if err := decoder.Decode(&raw); err != nil {
		return deploy.BundleSpec{}, fmt.Errorf("decode bundle spec: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return deploy.BundleSpec{}, errors.New("bundle spec contains trailing data")
	}
	duration, err := time.ParseDuration(strings.TrimSpace(raw.Duration))
	if err != nil {
		return deploy.BundleSpec{}, fmt.Errorf("parse duration: %w", err)
	}
	return deploy.BundleSpec{
		RunID: raw.RunID, SourceSHA: raw.SourceSHA, ScenarioPath: raw.ScenarioPath,
		ScenarioDigest: raw.ScenarioDigest, Duration: duration, PrivateIPv4: raw.PrivateIPv4,
		SimulatorSourceIPv4: raw.SimulatorSourceIPv4, PublicViewEnabled: raw.PublicObservation,
	}, nil
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
