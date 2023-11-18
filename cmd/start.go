package cmd

import (
	"github.com/spf13/cobra"
)

type startCMD struct {
}

func newStartCMD(ctx *WuKongIMContext) *startCMD {
	return &startCMD{}
}

func (s *startCMD) CMD() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "start the WuKongIM server",
		RunE:  s.run,
	}
	return cmd
}

func (s *startCMD) run(cmd *cobra.Command, args []string) error {
	ss, err := createSystemService(imserver)
	if err != nil {
		panic(err)
	}
	return ss.Start()
}
