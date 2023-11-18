package cmd

import (
	"github.com/spf13/cobra"
)

type stopCMD struct {
}

func newStopCMD(ctx *WuKongIMContext) *stopCMD {
	return &stopCMD{}
}

func (s *stopCMD) CMD() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "stop the WuKongIM server",
		RunE:  s.run,
	}
	return cmd
}

func (s *stopCMD) run(cmd *cobra.Command, args []string) error {
	ss, err := createSystemService(imserver)
	if err != nil {
		panic(err)
	}
	return ss.Stop()
}
