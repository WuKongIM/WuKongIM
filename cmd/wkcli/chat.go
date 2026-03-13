package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(chatCmd)

	chatCmd.Flags().String("uid", "", "user UID (required)")
	chatCmd.Flags().String("token", "", "user token (required)")
	chatCmd.Flags().String("channel", "", "channel ID to chat in")
	chatCmd.Flags().Int("channel_type", 2, "channel type: 2=group")
	_ = chatCmd.MarkFlagRequired("uid")
	_ = chatCmd.MarkFlagRequired("token")
}

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Interactive WebSocket chat",
	Long: `Start an interactive WebSocket chat session.
Connect to the server via WebSocket and send/receive messages in real-time.

Special commands:
  /quit    - Exit chat
  /ping    - Send ping to server`,
	Example: `  wkcli chat --uid user1 --token abc123 --channel group1 --channel_type 2`,
	RunE: func(cmd *cobra.Command, args []string) error {
		uid, _ := cmd.Flags().GetString("uid")
		token, _ := cmd.Flags().GetString("token")
		channel, _ := cmd.Flags().GetString("channel")
		channelType, _ := cmd.Flags().GetInt("channel_type")

		return runChat(uid, token, channel, channelType)
	},
}

type chatSession struct {
	conn        *websocket.Conn
	uid         string
	token       string
	channelID   string
	channelType int
	msgID       atomic.Int64
	mu          sync.Mutex
	done        chan struct{}
}

func runChat(uid, token, channel string, channelType int) error {
	// Step 1: Get route info.
	wsAddr, err := getWSAddr(uid)
	if err != nil {
		printError("Failed to get route info")
		return err
	}

	// Step 2: Connect via WebSocket.
	wsURL := wsAddr
	if !strings.HasPrefix(wsURL, "ws://") && !strings.HasPrefix(wsURL, "wss://") {
		wsURL = "ws://" + wsURL
	}
	fmt.Printf("%sConnecting to %s...%s\n", colorGray, wsURL, colorReset)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		printError(fmt.Sprintf("WebSocket connection failed: %v", err))
		return err
	}

	session := &chatSession{
		conn:        conn,
		uid:         uid,
		token:       token,
		channelID:   channel,
		channelType: channelType,
		done:        make(chan struct{}),
	}
	defer session.close()

	// Step 3: Authenticate.
	if err := session.authenticate(); err != nil {
		printError(fmt.Sprintf("Authentication failed: %v", err))
		return err
	}

	printSuccess(fmt.Sprintf("Connected to WuKongIM (%s)", wsURL))
	printSuccess(fmt.Sprintf("Authenticated as %s", uid))
	if channel != "" {
		printSuccess(fmt.Sprintf("Chatting in channel: %s (type: %d)", channel, channelType))
	}
	fmt.Println(colorGray + strings.Repeat("\u2500", 50) + colorReset)
	fmt.Println(colorGray + "Type a message and press Enter to send. Commands: /quit, /ping" + colorReset)

	// Step 4: Start heartbeat.
	go session.heartbeat()

	// Step 5: Start message receiver.
	go session.receiveMessages()

	// Step 6: Handle interrupt signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Step 7: Read user input.
	inputCh := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			inputCh <- scanner.Text()
		}
		close(inputCh)
	}()

	for {
		select {
		case <-session.done:
			return nil
		case <-sigCh:
			fmt.Println("\n" + colorGray + "Disconnecting..." + colorReset)
			return nil
		case text, ok := <-inputCh:
			if !ok {
				return nil
			}
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			if err := session.handleInput(text); err != nil {
				if err.Error() == "quit" {
					return nil
				}
				printError(fmt.Sprintf("Error: %v", err))
			}
		}
	}
}

func getWSAddr(uid string) (string, error) {
	path := fmt.Sprintf("/route?uid=%s", uid)
	body, statusCode, _, err := doGet(path)
	if err != nil {
		return "", err
	}
	if err := checkResponse(body, statusCode); err != nil {
		return "", err
	}

	var route struct {
		WSAddr string `json:"ws_addr"`
	}
	if err := json.Unmarshal(body, &route); err != nil {
		return "", fmt.Errorf("parse route response: %w", err)
	}
	if route.WSAddr == "" {
		return "", fmt.Errorf("no ws_addr in route response")
	}
	return route.WSAddr, nil
}

func (s *chatSession) nextID() string {
	return fmt.Sprintf("%d", s.msgID.Add(1))
}

func (s *chatSession) writeJSON(v interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(v)
}

func (s *chatSession) close() {
	s.conn.Close()
}

func (s *chatSession) authenticate() error {
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "connect",
		"id":      s.nextID(),
		"params": map[string]interface{}{
			"uid":             s.uid,
			"token":           s.token,
			"deviceFlag":      0,
			"deviceId":        "wkcli",
			"clientTimestamp":  time.Now().UnixMilli(),
			"version":         4,
		},
	}

	if err := s.writeJSON(req); err != nil {
		return fmt.Errorf("send connect: %w", err)
	}

	// Read connect response.
	s.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := s.conn.ReadMessage()
	s.conn.SetReadDeadline(time.Time{})
	if err != nil {
		return fmt.Errorf("read connack: %w", err)
	}

	var resp struct {
		ID     string `json:"id"`
		Result *struct {
			ReasonCode int `json:"reasonCode"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		return fmt.Errorf("parse connack: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("auth error (code=%d): %s", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result != nil && resp.Result.ReasonCode != 1 {
		return fmt.Errorf("auth failed with reason code: %d", resp.Result.ReasonCode)
	}
	return nil
}

func (s *chatSession) heartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			req := map[string]interface{}{
				"jsonrpc": "2.0",
				"method":  "ping",
				"id":      s.nextID(),
			}
			if err := s.writeJSON(req); err != nil {
				return
			}
		}
	}
}

func (s *chatSession) receiveMessages() {
	defer func() {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
	}()

	for {
		_, msg, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				fmt.Println("\n" + colorGray + "Connection closed by server." + colorReset)
			} else {
				select {
				case <-s.done:
					// Expected close.
				default:
					fmt.Printf("\n%s✗ Connection error: %v%s\n", colorRed, err, colorReset)
				}
			}
			return
		}

		s.handleServerMessage(msg)
	}
}

func (s *chatSession) handleServerMessage(msg []byte) {
	var probe struct {
		Method string          `json:"method"`
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(msg, &probe); err != nil {
		return
	}

	switch probe.Method {
	case "recv":
		s.handleRecvMessage(probe.Params)
	case "disconnect":
		var params struct {
			ReasonCode int    `json:"reasonCode"`
			Reason     string `json:"reason"`
		}
		if json.Unmarshal(probe.Params, &params) == nil {
			fmt.Printf("\n%s✗ Disconnected: %s (code=%d)%s\n", colorRed, params.Reason, params.ReasonCode, colorReset)
		}
	case "":
		// Response to a request (ping/send) - ignore silently.
	}
}

func (s *chatSession) handleRecvMessage(paramsRaw json.RawMessage) {
	var params struct {
		MessageID   string `json:"messageId"`
		MessageSeq  uint32 `json:"messageSeq"`
		FromUID     string `json:"fromUid"`
		ChannelID   string `json:"channelId"`
		ChannelType int    `json:"channelType"`
		Timestamp   int32  `json:"timestamp"`
		Payload     []byte `json:"payload"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		return
	}

	// Display the message.
	t := time.Unix(int64(params.Timestamp), 0).Format("15:04:05")
	fromLabel := params.FromUID
	msgColor := colorCyan
	if params.FromUID == s.uid {
		fromLabel = params.FromUID + " (you)"
		msgColor = colorGreen
	}

	// Try to extract readable content from payload.
	content := extractContent(params.Payload)

	fmt.Printf("\r%s[%s]%s %s%s%s: %s\n",
		colorGray, t, colorReset,
		msgColor, fromLabel, colorReset,
		content)
	fmt.Print("> ")

	// Send recvack.
	ack := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "recvack",
		"params": map[string]interface{}{
			"messageId":  params.MessageID,
			"messageSeq": params.MessageSeq,
		},
	}
	_ = s.writeJSON(ack)
}

func extractContent(payload []byte) string {
	if len(payload) == 0 {
		return "(empty)"
	}

	// Try to parse as JSON and extract "content" field.
	var obj map[string]interface{}
	if err := json.Unmarshal(payload, &obj); err == nil {
		if content, ok := obj["content"]; ok {
			return fmt.Sprintf("%v", content)
		}
		if text, ok := obj["text"]; ok {
			return fmt.Sprintf("%v", text)
		}
		// Return the full JSON if no known content field.
		b, _ := json.Marshal(obj)
		return string(b)
	}

	// Return raw string.
	return string(payload)
}

func (s *chatSession) handleInput(text string) error {
	switch {
	case text == "/quit":
		fmt.Println(colorGray + "Goodbye!" + colorReset)
		return fmt.Errorf("quit")
	case text == "/ping":
		req := map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "ping",
			"id":      s.nextID(),
		}
		if err := s.writeJSON(req); err != nil {
			return fmt.Errorf("send ping: %w", err)
		}
		fmt.Printf("%s  ping sent%s\n", colorGray, colorReset)
		return nil
	}

	// Send message.
	if s.channelID == "" {
		printError("No channel specified. Use --channel flag to specify a channel.")
		return nil
	}

	payload, _ := json.Marshal(map[string]string{
		"content": text,
	})

	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "send",
		"id":      s.nextID(),
		"params": map[string]interface{}{
			"channelId":   s.channelID,
			"channelType": s.channelType,
			"payload":     payload,
			"clientMsgNo": fmt.Sprintf("wkcli_%d", time.Now().UnixNano()),
		},
	}

	if err := s.writeJSON(req); err != nil {
		return fmt.Errorf("send message: %w", err)
	}

	return nil
}
