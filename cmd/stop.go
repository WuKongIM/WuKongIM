package cmd

import (
	"os"
	"os/exec"
	"path"

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
	strb, _ := os.ReadFile(path.Join(installDir, pidfile))
	command := exec.Command("kill", string(strb))
	err := command.Start()
	if err != nil {
		return err
	}
	return command.Wait()
}
