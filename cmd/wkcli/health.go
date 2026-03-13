package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(healthCmd)
}

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check server health",
	RunE: func(cmd *cobra.Command, args []string) error {
		body, statusCode, elapsed, err := doGet("/health")
		if err != nil {
			printError(fmt.Sprintf("Server is unreachable (%s)", getServerURL()))
			return err
		}

		if err := checkResponse(body, statusCode); err != nil {
			printError(fmt.Sprintf("Server is unhealthy (%s)", getServerURL()))
			return err
		}

		printSuccess(fmt.Sprintf("Server is healthy (%s)", getServerURL()))
		printInfo("Status", "ok")
		printInfo("Response time", elapsed.String())
		return nil
	},
}
