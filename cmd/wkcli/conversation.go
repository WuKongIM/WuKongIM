package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(conversationCmd)
	conversationCmd.AddCommand(convSyncCmd)
	conversationCmd.AddCommand(convChannelsCmd)
	conversationCmd.AddCommand(convDeleteCmd)
	conversationCmd.AddCommand(convClearUnreadCmd)
	conversationCmd.AddCommand(convSetUnreadCmd)

	// sync flags
	convSyncCmd.Flags().String("uid", "", "user UID (required)")
	convSyncCmd.Flags().Int("msg_count", 0, "recent messages per conversation")
	convSyncCmd.Flags().Int("only_unread", 0, "1=only unread conversations")
	convSyncCmd.Flags().Int("page", 0, "page number (0=no pagination)")
	convSyncCmd.Flags().Int("page_size", 100, "page size")
	_ = convSyncCmd.MarkFlagRequired("uid")

	// channels flags
	convChannelsCmd.Flags().String("uid", "", "user UID (required)")
	_ = convChannelsCmd.MarkFlagRequired("uid")

	// delete flags
	convDeleteCmd.Flags().String("uid", "", "user UID (required)")
	convDeleteCmd.Flags().String("channel_id", "", "channel ID (required)")
	convDeleteCmd.Flags().Int("channel_type", 2, "channel type")
	_ = convDeleteCmd.MarkFlagRequired("uid")
	_ = convDeleteCmd.MarkFlagRequired("channel_id")

	// clearunread flags
	convClearUnreadCmd.Flags().String("uid", "", "user UID (required)")
	convClearUnreadCmd.Flags().String("channel_id", "", "channel ID (required)")
	convClearUnreadCmd.Flags().Int("channel_type", 2, "channel type")
	_ = convClearUnreadCmd.MarkFlagRequired("uid")
	_ = convClearUnreadCmd.MarkFlagRequired("channel_id")

	// setunread flags
	convSetUnreadCmd.Flags().String("uid", "", "user UID (required)")
	convSetUnreadCmd.Flags().String("channel_id", "", "channel ID (required)")
	convSetUnreadCmd.Flags().Int("channel_type", 2, "channel type")
	convSetUnreadCmd.Flags().Int("unread", 0, "unread count (required)")
	_ = convSetUnreadCmd.MarkFlagRequired("uid")
	_ = convSetUnreadCmd.MarkFlagRequired("channel_id")
	_ = convSetUnreadCmd.MarkFlagRequired("unread")
}

var conversationCmd = &cobra.Command{
	Use:     "conversation",
	Aliases: []string{"conv"},
	Short:   "Conversation operations",
}

var convSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync user conversations",
	Example: `  wkcli conversation sync --uid user1
  wkcli conversation sync --uid user1 --only_unread 1 --msg_count 5`,
	RunE: func(cmd *cobra.Command, args []string) error {
		uid, _ := cmd.Flags().GetString("uid")
		msgCount, _ := cmd.Flags().GetInt("msg_count")
		onlyUnread, _ := cmd.Flags().GetInt("only_unread")
		page, _ := cmd.Flags().GetInt("page")
		pageSize, _ := cmd.Flags().GetInt("page_size")

		reqBody := map[string]any{
			"uid":         uid,
			"msg_count":   msgCount,
			"only_unread": onlyUnread,
			"page":        page,
			"page_size":   pageSize,
		}

		body, statusCode, _, err := doPost("/conversation/sync", reqBody)
		if err != nil {
			printError("Failed to sync conversations")
			return err
		}
		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to sync conversations")
			return err
		}

		var convs []struct {
			ChannelID      string `json:"channel_id"`
			ChannelType    uint8  `json:"channel_type"`
			Unread         int    `json:"unread"`
			Timestamp      int64  `json:"timestamp"`
			LastMsgSeq     uint32 `json:"last_msg_seq"`
			ReadedToMsgSeq uint32 `json:"readed_to_msg_seq"`
			Version        int64  `json:"version"`
		}
		if err := json.Unmarshal(body, &convs); err != nil {
			printJSON(body)
			return nil
		}

		printSuccess(fmt.Sprintf("Synced %d conversation(s) for user '%s'", len(convs), uid))
		fmt.Println()

		if len(convs) == 0 {
			fmt.Println("  No conversations found.")
			return nil
		}

		headers := []string{"Channel", "Type", "Unread", "LastSeq", "ReadTo", "Time"}
		var rows [][]string
		for _, c := range convs {
			t := ""
			if c.Timestamp > 0 {
				t = time.Unix(c.Timestamp, 0).Format("01-02 15:04")
			}
			rows = append(rows, []string{
				c.ChannelID,
				channelTypeName(int(c.ChannelType)),
				fmt.Sprintf("%d", c.Unread),
				fmt.Sprintf("%d", c.LastMsgSeq),
				fmt.Sprintf("%d", c.ReadedToMsgSeq),
				t,
			})
		}
		printTable(headers, rows)
		return nil
	},
}

var convChannelsCmd = &cobra.Command{
	Use:     "channels",
	Short:   "List conversation channels for a user",
	Example: `  wkcli conversation channels --uid user1`,
	RunE: func(cmd *cobra.Command, args []string) error {
		uid, _ := cmd.Flags().GetString("uid")

		body, statusCode, _, err := doPost("/conversation/channels", map[string]any{
			"uid": uid,
		})
		if err != nil {
			printError("Failed to get conversation channels")
			return err
		}
		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to get conversation channels")
			return err
		}

		var channels []struct {
			ChannelID   string `json:"channel_id"`
			ChannelType uint8  `json:"channel_type"`
		}
		if err := json.Unmarshal(body, &channels); err != nil {
			printJSON(body)
			return nil
		}

		printSuccess(fmt.Sprintf("Found %d conversation channel(s) for user '%s'", len(channels), uid))
		fmt.Println()

		if len(channels) == 0 {
			fmt.Println("  No conversation channels found.")
			return nil
		}

		headers := []string{"Channel ID", "Type"}
		var rows [][]string
		for _, ch := range channels {
			rows = append(rows, []string{
				ch.ChannelID,
				channelTypeName(int(ch.ChannelType)),
			})
		}
		printTable(headers, rows)
		return nil
	},
}

var convDeleteCmd = &cobra.Command{
	Use:     "delete",
	Short:   "Delete a conversation",
	Example: `  wkcli conversation delete --uid user1 --channel_id group1 --channel_type 2`,
	RunE: func(cmd *cobra.Command, args []string) error {
		uid, _ := cmd.Flags().GetString("uid")
		channelID, _ := cmd.Flags().GetString("channel_id")
		channelType, _ := cmd.Flags().GetInt("channel_type")

		body, statusCode, _, err := doPost("/conversations/delete", map[string]any{
			"uid":          uid,
			"channel_id":   channelID,
			"channel_type": channelType,
		})
		if err != nil {
			printError("Failed to delete conversation")
			return err
		}
		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to delete conversation")
			return err
		}

		printSuccess(fmt.Sprintf("Conversation deleted (uid=%s, channel=%s)", uid, channelID))
		return nil
	},
}

var convClearUnreadCmd = &cobra.Command{
	Use:     "clearunread",
	Short:   "Clear conversation unread count",
	Example: `  wkcli conversation clearunread --uid user1 --channel_id group1 --channel_type 2`,
	RunE: func(cmd *cobra.Command, args []string) error {
		uid, _ := cmd.Flags().GetString("uid")
		channelID, _ := cmd.Flags().GetString("channel_id")
		channelType, _ := cmd.Flags().GetInt("channel_type")

		body, statusCode, _, err := doPost("/conversations/clearUnread", map[string]any{
			"uid":          uid,
			"channel_id":   channelID,
			"channel_type": channelType,
		})
		if err != nil {
			printError("Failed to clear unread")
			return err
		}
		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to clear unread")
			return err
		}

		printSuccess(fmt.Sprintf("Unread cleared (uid=%s, channel=%s)", uid, channelID))
		return nil
	},
}

var convSetUnreadCmd = &cobra.Command{
	Use:     "setunread",
	Short:   "Set conversation unread count",
	Example: `  wkcli conversation setunread --uid user1 --channel_id group1 --channel_type 2 --unread 5`,
	RunE: func(cmd *cobra.Command, args []string) error {
		uid, _ := cmd.Flags().GetString("uid")
		channelID, _ := cmd.Flags().GetString("channel_id")
		channelType, _ := cmd.Flags().GetInt("channel_type")
		unread, _ := cmd.Flags().GetInt("unread")

		body, statusCode, _, err := doPost("/conversations/setUnread", map[string]any{
			"uid":          uid,
			"channel_id":   channelID,
			"channel_type": channelType,
			"unread":       unread,
		})
		if err != nil {
			printError("Failed to set unread")
			return err
		}
		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to set unread")
			return err
		}

		printSuccess(fmt.Sprintf("Unread set to %d (uid=%s, channel=%s)", unread, uid, channelID))
		return nil
	},
}
