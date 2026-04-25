package gnet

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
)

const (
	wsGUID          = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	wsMaxHeaderSize = 8 << 10
)

type wsHandshakeResult struct {
	consumed int
	response []byte
}

type wsHandshakeFailure struct {
	response []byte
	err      error
}

func parseWSHandshake(buf []byte, expectedPath string) (*wsHandshakeResult, *wsHandshakeFailure, bool) {
	headerEnd := bytes.Index(buf, []byte("\r\n\r\n"))
	if headerEnd < 0 {
		if len(buf) > wsMaxHeaderSize {
			return nil, failWSHandshake(http.StatusRequestHeaderFieldsTooLarge, nil, "websocket handshake headers too large"), true
		}
		return nil, nil, false
	}

	reqBytes := append([]byte(nil), buf[:headerEnd+4]...)
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(reqBytes)))
	if err != nil {
		return nil, failWSHandshake(http.StatusBadRequest, nil, "invalid websocket handshake request"), true
	}
	defer req.Body.Close()

	if req.Method != http.MethodGet {
		return nil, failWSHandshake(http.StatusMethodNotAllowed, map[string]string{"Allow": http.MethodGet}, "websocket handshake requires GET"), true
	}

	path := expectedPath
	if path == "" {
		path = "/"
	}
	if req.URL == nil || req.URL.Path != path {
		return nil, failWSHandshake(http.StatusNotFound, nil, fmt.Sprintf("websocket path %q not found", path)), true
	}

	if !headerHasToken(req.Header, "Connection", "upgrade") {
		return nil, failWSHandshake(http.StatusBadRequest, nil, "missing websocket Connection upgrade token"), true
	}
	if !strings.EqualFold(strings.TrimSpace(req.Header.Get("Upgrade")), "websocket") {
		return nil, failWSHandshake(http.StatusBadRequest, nil, "missing websocket Upgrade header"), true
	}

	key := strings.TrimSpace(req.Header.Get("Sec-WebSocket-Key"))
	if !isValidWSKey(key) {
		return nil, failWSHandshake(http.StatusBadRequest, nil, "invalid websocket key"), true
	}

	version := strings.TrimSpace(req.Header.Get("Sec-WebSocket-Version"))
	if version != "13" {
		return nil, failWSHandshake(http.StatusUpgradeRequired, map[string]string{
			"Sec-WebSocket-Version": "13",
		}, fmt.Sprintf("unsupported websocket version %q", version)), true
	}

	return &wsHandshakeResult{
		consumed: headerEnd + 4,
		response: buildHTTPResponse(http.StatusSwitchingProtocols, map[string]string{
			"Upgrade":              "websocket",
			"Connection":           "Upgrade",
			"Sec-WebSocket-Accept": computeWSAccept(key),
		}, nil),
	}, nil, true
}

func failWSHandshake(status int, headers map[string]string, msg string) *wsHandshakeFailure {
	body := []byte(msg)
	return &wsHandshakeFailure{
		response: buildHTTPResponse(status, headers, body),
		err:      fmt.Errorf("gateway/transport/gnet: %s", msg),
	}
}

func buildHTTPResponse(status int, headers map[string]string, body []byte) []byte {
	statusText := http.StatusText(status)
	if statusText == "" {
		statusText = "Unknown Status"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", status, statusText)
	for key, value := range headers {
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(value)
		b.WriteString("\r\n")
	}
	if len(body) > 0 {
		fmt.Fprintf(&b, "Content-Length: %d\r\n", len(body))
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	}
	if !hasHeader(headers, "Connection") && status != http.StatusSwitchingProtocols {
		b.WriteString("Connection: close\r\n")
	}
	b.WriteString("\r\n")
	b.Write(body)
	return []byte(b.String())
}

func headerHasToken(header http.Header, key, want string) bool {
	for _, value := range header.Values(key) {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), want) {
				return true
			}
		}
	}
	return false
}

func hasHeader(headers map[string]string, key string) bool {
	for header := range headers {
		if strings.EqualFold(header, key) {
			return true
		}
	}
	return false
}

func isValidWSKey(key string) bool {
	if key == "" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(key)
	return err == nil && len(decoded) == 16
}

func computeWSAccept(key string) string {
	sum := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}
