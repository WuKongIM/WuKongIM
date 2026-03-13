package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
)

// doRequest performs an HTTP request to the WuKongIM server.
func doRequest(method, path string, body interface{}) ([]byte, int, time.Duration, error) {
	serverURL := getServerURL()
	if serverURL == "" {
		return nil, 0, 0, fmt.Errorf("server not configured. Run: wkcli context set --server <url>")
	}

	url := strings.TrimRight(serverURL, "/") + path

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	token := getToken()
	if token != "" {
		req.Header.Set("token", token)
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return nil, 0, elapsed, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, elapsed, fmt.Errorf("read response: %w", err)
	}

	return respBody, resp.StatusCode, elapsed, nil
}

// doGet performs a GET request.
func doGet(path string) ([]byte, int, time.Duration, error) {
	return doRequest(http.MethodGet, path, nil)
}

// doPost performs a POST request with a JSON body.
func doPost(path string, body interface{}) ([]byte, int, time.Duration, error) {
	return doRequest(http.MethodPost, path, body)
}

// printSuccess prints a success message with a green checkmark.
func printSuccess(msg string) {
	fmt.Printf("%s✓%s %s\n", colorGreen, colorReset, msg)
}

// printError prints an error message with a red cross.
func printError(msg string) {
	fmt.Printf("%s✗%s %s\n", colorRed, colorReset, msg)
}

// printInfo prints an info line with indentation.
func printInfo(key, value string) {
	fmt.Printf("  %-16s %s\n", key+":", value)
}

// printJSON pretty-prints JSON data.
func printJSON(data []byte) {
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		fmt.Println(string(data))
		return
	}
	fmt.Println(out.String())
}

// printTable prints a simple table with headers and rows.
func printTable(headers []string, rows [][]string) {
	if len(headers) == 0 {
		return
	}

	// Calculate column widths.
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Print header.
	var headerLine, separatorLine strings.Builder
	for i, h := range headers {
		if i > 0 {
			headerLine.WriteString(" | ")
			separatorLine.WriteString("-+-")
		}
		headerLine.WriteString(fmt.Sprintf("%-*s", widths[i], h))
		separatorLine.WriteString(strings.Repeat("-", widths[i]))
	}
	fmt.Printf(" %s\n", headerLine.String())
	fmt.Printf("-%s-\n", separatorLine.String())

	// Print rows.
	for _, row := range rows {
		var line strings.Builder
		for i := range headers {
			if i > 0 {
				line.WriteString(" | ")
			}
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			line.WriteString(fmt.Sprintf("%-*s", widths[i], cell))
		}
		fmt.Printf(" %s\n", line.String())
	}
}

// checkResponse checks if the HTTP response indicates success.
func checkResponse(body []byte, statusCode int) error {
	if statusCode >= 200 && statusCode < 300 {
		return nil
	}
	var errResp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Msg != "" {
		return fmt.Errorf("server error (code=%d): %s", errResp.Code, errResp.Msg)
	}
	return fmt.Errorf("server returned status %d: %s", statusCode, string(body))
}
