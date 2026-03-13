package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(userCmd)
	userCmd.AddCommand(userTokenCmd)
	userCmd.AddCommand(userOnlineCmd)

	userTokenCmd.Flags().String("uid", "", "user UID (required)")
	userTokenCmd.Flags().String("token", "", "user token (required)")
	userTokenCmd.Flags().Int("device_flag", 0, "device type: 0=APP, 1=WEB, 2=PC")
	userTokenCmd.Flags().Int("device_level", 0, "device level: 0=slave, 1=master")
	_ = userTokenCmd.MarkFlagRequired("uid")
	_ = userTokenCmd.MarkFlagRequired("token")

	userOnlineCmd.Flags().String("uids", "", "comma-separated UIDs (required)")
	_ = userOnlineCmd.MarkFlagRequired("uids")
}

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "User management",
}

var userTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Update user token",
	Example: `  wkcli user token --uid test1 --token abc123
  wkcli user token --uid test1 --token abc123 --device_flag 1 --device_level 1`,
	RunE: func(cmd *cobra.Command, args []string) error {
		uid, _ := cmd.Flags().GetString("uid")
		token, _ := cmd.Flags().GetString("token")
		deviceFlag, _ := cmd.Flags().GetInt("device_flag")
		deviceLevel, _ := cmd.Flags().GetInt("device_level")

		reqBody := map[string]interface{}{
			"uid":          uid,
			"token":        token,
			"device_flag":  deviceFlag,
			"device_level": deviceLevel,
		}

		body, statusCode, _, err := doPost("/user/token", reqBody)
		if err != nil {
			printError("Failed to update user token")
			return err
		}

		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to update user token")
			return err
		}

		printSuccess("User token updated successfully")
		printInfo("UID", uid)
		printInfo("Device", deviceFlagName(deviceFlag))
		printInfo("Level", deviceLevelName(deviceLevel))
		return nil
	},
}

var userOnlineCmd = &cobra.Command{
	Use:   "online",
	Short: "Check user online status",
	Example: `  wkcli user online --uids user1,user2,user3`,
	RunE: func(cmd *cobra.Command, args []string) error {
		uidsStr, _ := cmd.Flags().GetString("uids")
		uids := strings.Split(uidsStr, ",")

		body, statusCode, _, err := doPost("/user/onlinestatus", uids)
		if err != nil {
			printError("Failed to check online status")
			return err
		}

		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to check online status")
			return err
		}

		var results []struct {
			UID        string `json:"uid"`
			DeviceFlag int    `json:"device_flag"`
			Online     int    `json:"online"`
		}
		if err := json.Unmarshal(body, &results); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		printSuccess(fmt.Sprintf("Online status (%d users)", len(results)))
		fmt.Println()

		headers := []string{"UID", "Device", "Status"}
		var rows [][]string
		for _, r := range results {
			status := colorRed + "offline" + colorReset
			if r.Online == 1 {
				status = colorGreen + "online" + colorReset
			}
			rows = append(rows, []string{r.UID, deviceFlagName(r.DeviceFlag), status})
		}
		printTable(headers, rows)
		return nil
	},
}

func deviceFlagName(flag int) string {
	switch flag {
	case 0:
		return "APP"
	case 1:
		return "WEB"
	case 2:
		return "PC"
	default:
		return fmt.Sprintf("unknown(%d)", flag)
	}
}

func deviceLevelName(level int) string {
	switch level {
	case 0:
		return "slave"
	case 1:
		return "master"
	default:
		return fmt.Sprintf("unknown(%d)", level)
	}
}
