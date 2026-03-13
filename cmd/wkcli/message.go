package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(messageCmd)
	messageCmd.AddCommand(messageSendCmd)
	messageCmd.AddCommand(messageSyncCmd)

	// message send flags
	messageSendCmd.Flags().String("from", "", "sender UID (required)")
	messageSendCmd.Flags().String("channel_id", "", "channel ID (required)")
	messageSendCmd.Flags().Int("channel_type", 2, "channel type: 2=group")
	messageSendCmd.Flags().String("payload", "", "message payload as JSON (required)")
	_ = messageSendCmd.MarkFlagRequired("from")
	_ = messageSendCmd.MarkFlagRequired("channel_id")
	_ = messageSendCmd.MarkFlagRequired("payload")

	// message sync flags
	messageSyncCmd.Flags().String("channel_id", "", "channel ID (required)")
	messageSyncCmd.Flags().Int("channel_type", 2, "channel type: 2=group")
	messageSyncCmd.Flags().Uint32("start_seq", 0, "start message sequence")
	messageSyncCmd.Flags().Uint32("end_seq", 0, "end message sequence")
	messageSyncCmd.Flags().Int("limit", 20, "number of messages to fetch")
	messageSyncCmd.Flags().String("login_uid", "", "login user UID")
	_ = messageSyncCmd.MarkFlagRequired("channel_id")
}

var messageCmd = &cobra.Command{
	Use:   "message",
	Short: "Message operations",
}

var messageSendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send a message",
	Example: `  wkcli message send --from user1 --channel_id group1 --channel_type 2 --payload '{"content":"hello"}'`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fromUID, _ := cmd.Flags().GetString("from")
		channelID, _ := cmd.Flags().GetString("channel_id")
		channelType, _ := cmd.Flags().GetInt("channel_type")
		payloadStr, _ := cmd.Flags().GetString("payload")

		// Validate payload is valid JSON.
		if !json.Valid([]byte(payloadStr)) {
			return fmt.Errorf("invalid JSON payload")
		}

		// Server expects payload as base64-encoded bytes.
		payloadBase64 := base64.StdEncoding.EncodeToString([]byte(payloadStr))

		reqBody := map[string]interface{}{
			"header": map[string]interface{}{
				"red_dot": 1,
			},
			"from_uid":     fromUID,
			"channel_id":   channelID,
			"channel_type": channelType,
			"payload":      payloadBase64,
		}

		body, statusCode, _, err := doPost("/message/send", reqBody)
		if err != nil {
			printError("Failed to send message")
			return err
		}

		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to send message")
			return err
		}

		var resp struct {
			Data struct {
				MessageID   json.Number `json:"message_id"`
				ClientMsgNo string      `json:"client_msg_no"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err == nil && resp.Data.MessageID != "" {
			printSuccess("Message sent successfully")
			printInfo("Message ID", resp.Data.MessageID.String())
			printInfo("Client No", resp.Data.ClientMsgNo)
		} else {
			printSuccess("Message sent successfully")
		}
		return nil
	},
}

var messageSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync channel messages",
	Example: `  wkcli message sync --channel_id group1 --channel_type 2
  wkcli message sync --channel_id group1 --channel_type 2 --start_seq 0 --limit 50`,
	RunE: func(cmd *cobra.Command, args []string) error {
		channelID, _ := cmd.Flags().GetString("channel_id")
		channelType, _ := cmd.Flags().GetInt("channel_type")
		startSeq, _ := cmd.Flags().GetUint32("start_seq")
		endSeq, _ := cmd.Flags().GetUint32("end_seq")
		limit, _ := cmd.Flags().GetInt("limit")
		loginUID, _ := cmd.Flags().GetString("login_uid")

		reqBody := map[string]interface{}{
			"channel_id":        channelID,
			"channel_type":      channelType,
			"start_message_seq": startSeq,
			"end_message_seq":   endSeq,
			"limit":             limit,
		}
		if loginUID != "" {
			reqBody["login_uid"] = loginUID
		}

		body, statusCode, _, err := doPost("/channel/messagesync", reqBody)
		if err != nil {
			printError("Failed to sync messages")
			return err
		}

		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to sync messages")
			return err
		}

		var resp struct {
			StartMessageSeq uint32 `json:"start_message_seq"`
			EndMessageSeq   uint32 `json:"end_message_seq"`
			More            int    `json:"more"`
			Messages        []struct {
				MessageID   json.Number `json:"message_id"`
				ClientMsgNo string      `json:"client_msg_no"`
				FromUID     string      `json:"from_uid"`
				ChannelID   string      `json:"channel_id"`
				ChannelType int         `json:"channel_type"`
				Payload     string      `json:"payload"` // base64 encoded
				MessageSeq  uint32      `json:"message_seq"`
				Timestamp   int64       `json:"timestamp"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			printJSON(body)
			return nil
		}

		msgs := resp.Messages
		printSuccess(fmt.Sprintf("Synced %d message(s) from channel '%s'", len(msgs), channelID))
		if resp.More == 1 {
			fmt.Printf("  %s(more messages available)%s\n", colorYellow, colorReset)
		}
		fmt.Println()

		if len(msgs) == 0 {
			fmt.Println("  No messages found.")
			return nil
		}

		headers := []string{"Seq", "From", "Payload", "Time"}
		var rows [][]string
		for _, m := range msgs {
			payloadStr := m.Payload
			// Decode base64 payload.
			if decoded, err := base64.StdEncoding.DecodeString(m.Payload); err == nil {
				payloadStr = string(decoded)
			}
			if len(payloadStr) > 60 {
				payloadStr = payloadStr[:60] + "..."
			}
			t := time.Unix(m.Timestamp, 0).Format("15:04:05")
			rows = append(rows, []string{
				fmt.Sprintf("%d", m.MessageSeq),
				m.FromUID,
				payloadStr,
				t,
			})
		}
		printTable(headers, rows)
		return nil
	},
}
