// Command wkcloudgate evaluates the fail-closed cloud Bootstrap Gate.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/WuKongIM/WuKongIM/internal/infra/cloudsim/deploy"
)

const maxSnapshotBytes = 256 << 10

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
	var snapshotPath, digest string
	root := &cobra.Command{
		Use: "wkcloudgate", Short: "Evaluate the exact three-node cloud Bootstrap Gate",
		Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
		RunE: func(*cobra.Command, []string) error {
			snapshot, err := readSnapshot(snapshotPath)
			if err != nil {
				return err
			}
			result := deploy.EvaluateBootstrapGate(snapshot, digest)
			encoder := json.NewEncoder(stdout)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(result); err != nil {
				return err
			}
			if !result.Passed {
				return errors.New("bootstrap gate failed")
			}
			return nil
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.Flags().StringVar(&snapshotPath, "snapshot", "", "strict BootstrapSnapshot JSON path")
	root.Flags().StringVar(&digest, "bundle-digest", "", "expected sha256 bundle digest")
	_ = root.MarkFlagRequired("snapshot")
	_ = root.MarkFlagRequired("bundle-digest")
	return root
}

func readSnapshot(path string) (deploy.BootstrapSnapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return deploy.BootstrapSnapshot{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxSnapshotBytes+1))
	decoder.DisallowUnknownFields()
	var snapshot deploy.BootstrapSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return deploy.BootstrapSnapshot{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return deploy.BootstrapSnapshot{}, errors.New("bootstrap snapshot contains trailing data")
	}
	return snapshot, nil
}
