package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(channelCmd)
	channelCmd.AddCommand(channelCreateCmd)
	channelCmd.AddCommand(channelDeleteCmd)
	channelCmd.AddCommand(channelInfoCmd)
	channelCmd.AddCommand(subscribersCmd)
	subscribersCmd.AddCommand(subscribersAddCmd)
	subscribersCmd.AddCommand(subscribersRemoveCmd)

	// channel create flags
	channelCreateCmd.Flags().String("channel_id", "", "channel ID (required)")
	channelCreateCmd.Flags().Int("channel_type", 2, "channel type: 2=group")
	channelCreateCmd.Flags().String("subscribers", "", "comma-separated subscriber UIDs")
	_ = channelCreateCmd.MarkFlagRequired("channel_id")

	// channel delete flags
	channelDeleteCmd.Flags().String("channel_id", "", "channel ID (required)")
	channelDeleteCmd.Flags().Int("channel_type", 2, "channel type: 2=group")
	_ = channelDeleteCmd.MarkFlagRequired("channel_id")

	// channel info flags
	channelInfoCmd.Flags().String("channel_id", "", "channel ID (required)")
	channelInfoCmd.Flags().Int("channel_type", 2, "channel type")
	channelInfoCmd.Flags().Int("ban", -1, "ban channel: 0=no, 1=yes")
	channelInfoCmd.Flags().Int("large", -1, "large group: 0=no, 1=yes")
	_ = channelInfoCmd.MarkFlagRequired("channel_id")

	// subscribers add flags
	subscribersAddCmd.Flags().String("channel_id", "", "channel ID (required)")
	subscribersAddCmd.Flags().Int("channel_type", 2, "channel type: 2=group")
	subscribersAddCmd.Flags().String("uids", "", "comma-separated UIDs to add (required)")
	_ = subscribersAddCmd.MarkFlagRequired("channel_id")
	_ = subscribersAddCmd.MarkFlagRequired("uids")

	// subscribers remove flags
	subscribersRemoveCmd.Flags().String("channel_id", "", "channel ID (required)")
	subscribersRemoveCmd.Flags().Int("channel_type", 2, "channel type: 2=group")
	subscribersRemoveCmd.Flags().String("uids", "", "comma-separated UIDs to remove (required)")
	_ = subscribersRemoveCmd.MarkFlagRequired("channel_id")
	_ = subscribersRemoveCmd.MarkFlagRequired("uids")
}

var channelCmd = &cobra.Command{
	Use:   "channel",
	Short: "Channel management",
}

var channelCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a channel",
	Example: `  wkcli channel create --channel_id group1 --channel_type 2
  wkcli channel create --channel_id group1 --channel_type 2 --subscribers u1,u2,u3`,
	RunE: func(cmd *cobra.Command, args []string) error {
		channelID, _ := cmd.Flags().GetString("channel_id")
		channelType, _ := cmd.Flags().GetInt("channel_type")
		subscribersStr, _ := cmd.Flags().GetString("subscribers")

		reqBody := map[string]interface{}{
			"channel_id":   channelID,
			"channel_type": channelType,
		}

		if subscribersStr != "" {
			reqBody["subscribers"] = strings.Split(subscribersStr, ",")
		}

		body, statusCode, _, err := doPost("/channel", reqBody)
		if err != nil {
			printError("Failed to create channel")
			return err
		}

		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to create channel")
			return err
		}

		printSuccess("Channel created successfully")
		printInfo("Channel ID", channelID)
		printInfo("Channel Type", channelTypeName(channelType))
		if subscribersStr != "" {
			printInfo("Subscribers", subscribersStr)
		}
		return nil
	},
}

var channelDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a channel",
	Example: `  wkcli channel delete --channel_id group1 --channel_type 2`,
	RunE: func(cmd *cobra.Command, args []string) error {
		channelID, _ := cmd.Flags().GetString("channel_id")
		channelType, _ := cmd.Flags().GetInt("channel_type")

		reqBody := map[string]interface{}{
			"channel_id":   channelID,
			"channel_type": channelType,
		}

		body, statusCode, _, err := doPost("/channel/delete", reqBody)
		if err != nil {
			printError("Failed to delete channel")
			return err
		}

		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to delete channel")
			return err
		}

		printSuccess("Channel deleted successfully")
		printInfo("Channel ID", channelID)
		printInfo("Channel Type", channelTypeName(channelType))
		return nil
	},
}

var channelInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Update channel info",
	Example: `  wkcli channel info --channel_id group1 --channel_type 2 --ban 1`,
	RunE: func(cmd *cobra.Command, args []string) error {
		channelID, _ := cmd.Flags().GetString("channel_id")
		channelType, _ := cmd.Flags().GetInt("channel_type")
		ban, _ := cmd.Flags().GetInt("ban")
		large, _ := cmd.Flags().GetInt("large")

		reqBody := map[string]interface{}{
			"channel_id":   channelID,
			"channel_type": channelType,
		}
		if ban >= 0 {
			reqBody["ban"] = ban
		}
		if large >= 0 {
			reqBody["large"] = large
		}

		body, statusCode, _, err := doPost("/channel/info", reqBody)
		if err != nil {
			printError("Failed to update channel info")
			return err
		}

		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to update channel info")
			return err
		}

		printSuccess("Channel info updated successfully")
		printInfo("Channel ID", channelID)
		return nil
	},
}

var subscribersCmd = &cobra.Command{
	Use:   "subscribers",
	Short: "Manage channel subscribers",
}

var subscribersAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add subscribers to channel",
	Example: `  wkcli channel subscribers add --channel_id group1 --channel_type 2 --uids u1,u2`,
	RunE: func(cmd *cobra.Command, args []string) error {
		channelID, _ := cmd.Flags().GetString("channel_id")
		channelType, _ := cmd.Flags().GetInt("channel_type")
		uidsStr, _ := cmd.Flags().GetString("uids")
		uids := strings.Split(uidsStr, ",")

		reqBody := map[string]interface{}{
			"channel_id":   channelID,
			"channel_type": channelType,
			"subscribers":  uids,
		}

		body, statusCode, _, err := doPost("/channel/subscriber_add", reqBody)
		if err != nil {
			printError("Failed to add subscribers")
			return err
		}

		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to add subscribers")
			return err
		}

		printSuccess(fmt.Sprintf("Added %d subscriber(s) to channel '%s'", len(uids), channelID))
		return nil
	},
}

var subscribersRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove subscribers from channel",
	Example: `  wkcli channel subscribers remove --channel_id group1 --channel_type 2 --uids u1`,
	RunE: func(cmd *cobra.Command, args []string) error {
		channelID, _ := cmd.Flags().GetString("channel_id")
		channelType, _ := cmd.Flags().GetInt("channel_type")
		uidsStr, _ := cmd.Flags().GetString("uids")
		uids := strings.Split(uidsStr, ",")

		reqBody := map[string]interface{}{
			"channel_id":   channelID,
			"channel_type": channelType,
			"subscribers":  uids,
		}

		body, statusCode, _, err := doPost("/channel/subscriber_remove", reqBody)
		if err != nil {
			printError("Failed to remove subscribers")
			return err
		}

		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to remove subscribers")
			return err
		}

		printSuccess(fmt.Sprintf("Removed %d subscriber(s) from channel '%s'", len(uids), channelID))
		return nil
	},
}

func channelTypeName(t int) string {
	switch t {
	case 1:
		return "group"
	case 2:
		return "person"
	case 3:
		return "live"
	case 4:
		return "temp"
	case 5:
		return "agent"
	default:
		return fmt.Sprintf("unknown(%d)", t)
	}
}
