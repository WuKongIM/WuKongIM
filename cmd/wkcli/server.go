package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.AddCommand(serverVarzCmd)
	serverCmd.AddCommand(serverConnzCmd)

	// connz flags
	serverConnzCmd.Flags().String("uid", "", "filter by UID")
	serverConnzCmd.Flags().Int("limit", 20, "number of connections to show")
	serverConnzCmd.Flags().Int("offset", 0, "pagination offset")
	serverConnzCmd.Flags().String("sort", "", "sort by: id, inMsg, outMsg, uptime, idle")
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Server status",
}

var serverVarzCmd = &cobra.Command{
	Use:   "varz",
	Short: "Show system variables",
	RunE: func(cmd *cobra.Command, args []string) error {
		body, statusCode, _, err := doGet("/varz")
		if err != nil {
			printError("Failed to get server variables")
			return err
		}

		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to get server variables")
			return err
		}

		var varz struct {
			ServerID    string  `json:"server_id"`
			ServerName  string  `json:"server_name"`
			Version     string  `json:"version"`
			Connections int     `json:"connections"`
			Uptime      string  `json:"uptime"`
			CPU         float64 `json:"cpu"`
			Goroutine   int     `json:"goroutine"`
			Mem         int64   `json:"mem"`
			InMsgs      int64   `json:"in_msgs"`
			OutMsgs     int64   `json:"out_msgs"`
			InBytes     int64   `json:"in_bytes"`
			OutBytes    int64   `json:"out_bytes"`
			TCPAddr     string  `json:"tcp_addr"`
			WSAddr      string  `json:"ws_addr"`
			WSSAddr     string  `json:"wss_addr"`
			Commit      string  `json:"commit"`
			CommitDate  string  `json:"commit_date"`
		}
		if err := json.Unmarshal(body, &varz); err != nil {
			printJSON(body)
			return nil
		}

		printSuccess("Server Variables")
		fmt.Println()

		headers := []string{"Key", "Value"}
		rows := [][]string{
			{"Server ID", varz.ServerID},
			{"Version", varz.Version},
			{"Uptime", varz.Uptime},
			{"Connections", fmt.Sprintf("%d", varz.Connections)},
			{"CPU", fmt.Sprintf("%.1f%%", varz.CPU)},
			{"Goroutines", fmt.Sprintf("%d", varz.Goroutine)},
			{"Memory", formatBytes(varz.Mem)},
			{"In Messages", fmt.Sprintf("%d", varz.InMsgs)},
			{"Out Messages", fmt.Sprintf("%d", varz.OutMsgs)},
			{"In Bytes", formatBytes(varz.InBytes)},
			{"Out Bytes", formatBytes(varz.OutBytes)},
			{"TCP Addr", varz.TCPAddr},
			{"WS Addr", varz.WSAddr},
			{"WSS Addr", varz.WSSAddr},
			{"Commit", varz.Commit},
			{"Commit Date", varz.CommitDate},
		}
		printTable(headers, rows)
		return nil
	},
}

var serverConnzCmd = &cobra.Command{
	Use:   "connz",
	Short: "Show connection information",
	Example: `  wkcli server connz
  wkcli server connz --uid user1 --limit 10
  wkcli server connz --sort uptime --limit 50`,
	RunE: func(cmd *cobra.Command, args []string) error {
		uid, _ := cmd.Flags().GetString("uid")
		limit, _ := cmd.Flags().GetInt("limit")
		offset, _ := cmd.Flags().GetInt("offset")
		sort, _ := cmd.Flags().GetString("sort")

		path := fmt.Sprintf("/connz?limit=%d&offset=%d", limit, offset)
		if uid != "" {
			path += "&uid=" + uid
		}
		if sort != "" {
			path += "&sort=" + sort
		}

		body, statusCode, _, err := doGet(path)
		if err != nil {
			printError("Failed to get connection info")
			return err
		}

		if err := checkResponse(body, statusCode); err != nil {
			printError("Failed to get connection info")
			return err
		}

		var connz struct {
			Connections []struct {
				ID          int64  `json:"id"`
				UID         string `json:"uid"`
				IP          string `json:"ip"`
				Port        int    `json:"port"`
				Uptime      string `json:"uptime"`
				Idle        string `json:"idle"`
				InMsgs      int64  `json:"in_msgs"`
				OutMsgs     int64  `json:"out_msgs"`
				Device      string `json:"device"`
				DeviceID    string `json:"device_id"`
				Version     int    `json:"version"`
				InMsgBytes  int64  `json:"in_msg_bytes"`
				OutMsgBytes int64  `json:"out_msg_bytes"`
			} `json:"connections"`
			Total  int    `json:"total"`
			Offset int    `json:"offset"`
			Limit  int    `json:"limit"`
			Now    string `json:"now"`
		}
		if err := json.Unmarshal(body, &connz); err != nil {
			printJSON(body)
			return nil
		}

		printSuccess(fmt.Sprintf("Connections (%d total, showing %d-%d)",
			connz.Total, connz.Offset+1,
			min(connz.Offset+connz.Limit, connz.Total)))
		fmt.Println()

		if len(connz.Connections) == 0 {
			fmt.Println("  No connections found.")
			return nil
		}

		headers := []string{"UID", "Device", "IP", "Uptime", "In Msgs", "Out Msgs"}
		var rows [][]string
		for _, c := range connz.Connections {
			rows = append(rows, []string{
				c.UID,
				c.Device,
				fmt.Sprintf("%s:%d", c.IP, c.Port),
				c.Uptime,
				fmt.Sprintf("%d", c.InMsgs),
				fmt.Sprintf("%d", c.OutMsgs),
			})
		}
		printTable(headers, rows)
		return nil
	},
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
