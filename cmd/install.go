package cmd

import (
	"github.com/kardianos/service"
	"github.com/spf13/cobra"
)

type installCMD struct {
}

func newInstallCMD(ctx *WuKongIMContext) *installCMD {
	return &installCMD{}
}

func (s *installCMD) CMD() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "install the WuKongIM server",
		RunE:  s.run,
	}
	return cmd
}

func (s *installCMD) run(cmd *cobra.Command, args []string) error {
	ss, err := createSystemService(imserver)
	if err != nil {
		panic(err)
	}

	return service.Control(ss, "install")
}
