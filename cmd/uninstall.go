package cmd

import "github.com/spf13/cobra"

type uninstallCMD struct {
}

func newUninstallCMD(ctx *WuKongIMContext) *uninstallCMD {
	return &uninstallCMD{}
}

func (s *uninstallCMD) CMD() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "uninstall the WuKongIM server",
		RunE:  s.run,
	}
	return cmd
}

func (s *uninstallCMD) run(cmd *cobra.Command, args []string) error {
	ss, err := createSystemService(imserver)
	if err != nil {
		panic(err)
	}

	return ss.Uninstall()
}
