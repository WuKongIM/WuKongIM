package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path"

	"github.com/spf13/cobra"
)

type stopCMD struct {
	ctx *WuKongIMContext
}

func newStopCMD(ctx *WuKongIMContext) *stopCMD {
	return &stopCMD{
		ctx: ctx,
	}
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
		fmt.Println("Error: ", err)
		return err
	}
	err = command.Wait()
	if err != nil {
		fmt.Println("Error: ", err)
		return err
	}
	fmt.Println("WuKongIM server stopped")
	return nil
}
