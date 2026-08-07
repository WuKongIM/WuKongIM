package main

import (
	"io"

	"github.com/spf13/cobra"

	clouddeployinfra "github.com/WuKongIM/WuKongIM/internal/infra/clouddeploy"
	clouddeploy "github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
)

func addOfflineCommands(root *cobra.Command, stdout io.Writer) {
	var sealRoot, sourceSHA, controlSHA string
	seal := &cobra.Command{
		Use: "seal-offline", Short: "Seal the procurement-independent Ubuntu deployment bundle", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			directory, err := clouddeployinfra.Open(sealRoot)
			if err != nil {
				return err
			}
			manifest, err := clouddeploy.Seal(directory, sourceSHA, controlSHA)
			if err != nil {
				return err
			}
			return writeJSON(stdout, manifest)
		},
	}
	seal.Flags().StringVar(&sealRoot, "root", "", "offline bundle root containing prebuilt files")
	seal.Flags().StringVar(&sourceSHA, "source-sha", "", "immutable product source commit")
	seal.Flags().StringVar(&controlSHA, "control-sha", "", "trusted workflow control commit")
	for _, name := range []string{"root", "source-sha", "control-sha"} {
		_ = seal.MarkFlagRequired(name)
	}

	var verifyRoot string
	verify := &cobra.Command{
		Use: "verify-offline", Short: "Independently verify an offline deployment bundle", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			directory, err := clouddeployinfra.Open(verifyRoot)
			if err != nil {
				return err
			}
			manifest, err := clouddeploy.Verify(directory)
			if err != nil {
				return err
			}
			return writeJSON(stdout, manifest)
		},
	}
	verify.Flags().StringVar(&verifyRoot, "root", "", "extracted offline bundle root")
	_ = verify.MarkFlagRequired("root")
	root.AddCommand(seal, verify)
}
