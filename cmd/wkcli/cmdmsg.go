package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(cmdCmd)
	cmdCmd.AddCommand(cmdSendCmd)
	cmdCmd.AddCommand(cmdSyncCmd)
	cmdCmd.AddCommand(cmdSyncackCmd)

	// cmd send flags
	cmdSendCmd.Flags().String("from", "", "sender UID (required)")
	cmdSendCmd.Flags().String("channel_id", "", "channel ID (required)")
	cmdSendCmd.Flags().Int("channel_type", 2, "channel type: 2=group")
	cmdSendCmd.Flags().String("payload", "", "message payload as JSON (required)")
	_ = cmdSendCmd.MarkFlagRequired("from")
	_ = cmdSendCmd.MarkFlagRequired("channel_id")
	_ = cmdSendCmd.MarkFlagRequired("payload")

	// cmd sync flags
	cmdSyncCmd.Flags().String("uid", "", "user UID (required)")
	cmdSyncCmd.Flags().Uint64("message_seq", 0, "client max message sequence")
	cmdSyncCmd.Flags().Int("limit", 200, "message count limit")
	_ = cmdSyncCmd.MarkFlagRequired("uid")

	// cmd syncack flags
	cmdSyncackCmd.Flags().String("uid", "", "user UID (required)")
	cmdSyncackCmd.Flags().Uint64("last_message_seq", 0, "last synced message seq (required)")
	_ = cmdSyncackCmd.MarkFlagRequired("uid")
	_ = cmdSyncackCmd.MarkFlagRequired("last_message_seq")
}

var cmdCmd = &cobra.Command{
	Use:   "cmd",
	Short: "Command message operations (sync_once=1)",
}

var cmdSendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send a command message (header.sync_once=1)",
	Example: `  wkcli cmd send --from user1 --channel_id group1 --channel_type 2 --payload '{"type":"typing"}'`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fromUID, _ := cmd.Flags().GetString("from")
		channelID, _ := cmd.Flags().GetString("channel_id")
		channelType, _ := cmd.Flags().GetInt("channel_type")
		payloadStr, _ := cmd.Flags().GetString("payload")

		if !json.Valid([]byte(payloadStr)) {
			return fmt.Errorf("invalid JSON payload")
		}

		payloadBase64 := base64.StdEncoding.EncodeToString([]byte(payloadStr))

		reqBody := map[string]any{
			"header": map[string]any{
				"red_dot":   0,
				"sync_once": 1,
			},
			"from_uid":     fromUID,
			"channel_id":   channelID,
			"channel_type": channelType,
			"payload":      payloadBase64,
		}

		body, statusCode, _, err := doPost("/message/send", reqBody)
		if err != nil {
			printError("Failed to send command message")
			return err
		}
		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to send command message")
			return err
		}

		var resp struct {
			Data struct {
				MessageID   json.Number `json:"message_id"`
				ClientMsgNo string      `json:"client_msg_no"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err == nil && resp.Data.MessageID != "" {
			printSuccess("Command message sent successfully")
			printInfo("Message ID", resp.Data.MessageID.String())
			printInfo("Client No", resp.Data.ClientMsgNo)
		} else {
			printSuccess("Command message sent successfully")
		}
		return nil
	},
}

var cmdSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync command messages for a user",
	Example: `  wkcli cmd sync --uid user1
  wkcli cmd sync --uid user1 --message_seq 100 --limit 50`,
	RunE: func(cmd *cobra.Command, args []string) error {
		uid, _ := cmd.Flags().GetString("uid")
		messageSeq, _ := cmd.Flags().GetUint64("message_seq")
		limit, _ := cmd.Flags().GetInt("limit")

		reqBody := map[string]any{
			"uid":         uid,
			"message_seq": messageSeq,
			"limit":       limit,
		}

		body, statusCode, _, err := doPost("/message/sync", reqBody)
		if err != nil {
			printError("Failed to sync command messages")
			return err
		}
		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to sync command messages")
			return err
		}

		var msgs []struct {
			Header struct {
				NoPersist int `json:"no_persist"`
				RedDot    int `json:"red_dot"`
				SyncOnce  int `json:"sync_once"`
			} `json:"header"`
			MessageID   json.Number `json:"message_id"`
			ClientMsgNo string      `json:"client_msg_no"`
			MessageSeq  uint64      `json:"message_seq"`
			FromUID     string      `json:"from_uid"`
			ChannelID   string      `json:"channel_id"`
			ChannelType uint8       `json:"channel_type"`
			Timestamp   int32       `json:"timestamp"`
			Payload     string      `json:"payload"` // base64
		}
		if err := json.Unmarshal(body, &msgs); err != nil {
			printJSON(body)
			return nil
		}

		printSuccess(fmt.Sprintf("Synced %d command message(s) for user '%s'", len(msgs), uid))
		fmt.Println()

		if len(msgs) == 0 {
			fmt.Println("  No command messages found.")
			return nil
		}

		headers := []string{"Seq", "From", "Channel", "Payload", "Time"}
		var rows [][]string
		for _, m := range msgs {
			payloadStr := m.Payload
			if decoded, err := base64.StdEncoding.DecodeString(m.Payload); err == nil {
				payloadStr = string(decoded)
			}
			if len(payloadStr) > 50 {
				payloadStr = payloadStr[:50] + "..."
			}
			t := time.Unix(int64(m.Timestamp), 0).Format("15:04:05")
			rows = append(rows, []string{
				fmt.Sprintf("%d", m.MessageSeq),
				m.FromUID,
				fmt.Sprintf("%s(%d)", m.ChannelID, m.ChannelType),
				payloadStr,
				t,
			})
		}
		printTable(headers, rows)
		return nil
	},
}

var cmdSyncackCmd = &cobra.Command{
	Use:   "syncack",
	Short: "Acknowledge synced command messages",
	Example: `  wkcli cmd syncack --uid user1 --last_message_seq 100`,
	RunE: func(cmd *cobra.Command, args []string) error {
		uid, _ := cmd.Flags().GetString("uid")
		lastMessageSeq, _ := cmd.Flags().GetUint64("last_message_seq")

		reqBody := map[string]any{
			"uid":              uid,
			"last_message_seq": lastMessageSeq,
		}

		body, statusCode, _, err := doPost("/message/syncack", reqBody)
		if err != nil {
			printError("Failed to send syncack")
			return err
		}
		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to send syncack")
			return err
		}

		printSuccess(fmt.Sprintf("Syncack sent for user '%s' (last_message_seq=%d)", uid, lastMessageSeq))
		return nil
	},
}
