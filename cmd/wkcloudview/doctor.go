package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/WuKongIM/WuKongIM/internal/access/cloudview"
)

const maxDoctorResponseBytes = 1 << 20

type doctorOptions struct {
	// BaseURL is the public HTTP Cloud View origin.
	BaseURL string
	// Username is the fixed Cloud Simulation Manager username.
	Username string
	// Password is the fixed Cloud Simulation Manager password.
	Password string
	// GateToken authenticates this automated probe without changing purity state.
	GateToken string
	// ExpectedTargets is the exact required active and healthy target count.
	ExpectedTargets int
	// WebSocketPath optionally selects a test-only WebSocket path.
	WebSocketPath string
}

type doctorResult struct {
	// Manager reports that the UI, login, permissions, and nodes API passed.
	Manager bool `json:"manager"`
	// Demo reports that the embedded Demo UI passed.
	Demo bool `json:"demo"`
	// RouteRewrite reports that the public WebSocket origin was advertised.
	RouteRewrite bool `json:"route_rewrite"`
	// WebSocket reports that a real public upgrade completed.
	WebSocket bool `json:"websocket"`
	// PrometheusTargetsUp is the exact active and healthy target count.
	PrometheusTargetsUp int `json:"prometheus_targets_up"`
}

type doctorTransport struct {
	base      http.RoundTripper
	gateToken string
}

func (t doctorTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request.Header.Set(cloudview.GateProbeHeader, t.gateToken)
	return t.base.RoundTrip(request)
}

func runDoctor(ctx context.Context, options doctorOptions) (doctorResult, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(options.BaseURL), "/"))
	if err != nil || base.Scheme != "http" || base.Host == "" || base.Path != "" {
		return doctorResult{}, errors.New("doctor base URL must be an HTTP origin")
	}
	if strings.TrimSpace(options.Username) == "" || options.Password == "" ||
		strings.TrimSpace(options.GateToken) == "" || options.ExpectedTargets <= 0 {
		return doctorResult{}, errors.New("doctor credentials, gate token, and expected targets are required")
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: doctorTransport{
		base: http.DefaultTransport, gateToken: options.GateToken,
	}}
	result := doctorResult{}
	if err := doctorGET(ctx, client, base.String()+"/", ""); err != nil {
		return result, fmt.Errorf("manager UI: %w", err)
	}
	result.Manager = true
	if err := doctorGET(ctx, client, base.String()+"/demo/", ""); err != nil {
		return result, fmt.Errorf("demo UI: %w", err)
	}
	result.Demo = true

	credentials, _ := json.Marshal(map[string]string{"username": options.Username, "password": options.Password})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String()+"/manager/login", bytes.NewReader(credentials))
	if err != nil {
		return result, err
	}
	request.Header.Set("Content-Type", "application/json")
	var login struct {
		AccessToken string `json:"access_token"`
		Permissions []struct {
			Resource string   `json:"resource"`
			Actions  []string `json:"actions"`
		} `json:"permissions"`
	}
	if err := doctorJSON(client, request, &login); err != nil {
		return result, fmt.Errorf("manager login: %w", err)
	}
	if login.AccessToken == "" || !hasWildcardPermission(login.Permissions) {
		return result, errors.New("manager login lacks wildcard permission")
	}
	if err := doctorGET(ctx, client, base.String()+"/manager/nodes", "Bearer "+login.AccessToken); err != nil {
		return result, fmt.Errorf("manager nodes: %w", err)
	}

	var route struct {
		WSAddr string `json:"ws_addr"`
	}
	routeRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.String()+"/route?uid=cloud-view-doctor", nil)
	if err := doctorJSON(client, routeRequest, &route); err != nil {
		return result, fmt.Errorf("demo route: %w", err)
	}
	expectedWSOrigin := "ws://" + base.Host
	if route.WSAddr != expectedWSOrigin {
		return result, fmt.Errorf("demo route ws_addr=%q want=%q", route.WSAddr, expectedWSOrigin)
	}
	result.RouteRewrite = true

	webSocketPath := options.WebSocketPath
	if webSocketPath == "" {
		webSocketPath = "/"
	}
	if !strings.HasPrefix(webSocketPath, "/") {
		return result, errors.New("WebSocket path must be absolute")
	}
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second
	webSocketHeader := http.Header{}
	webSocketHeader.Set(cloudview.GateProbeHeader, options.GateToken)
	connection, response, err := dialer.DialContext(ctx, expectedWSOrigin+webSocketPath, webSocketHeader)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return result, fmt.Errorf("public websocket handshake: %w", err)
	}
	_ = connection.Close()
	result.WebSocket = true

	var targets struct {
		Status string `json:"status"`
		Data   struct {
			ActiveTargets []struct {
				Health string `json:"health"`
			} `json:"activeTargets"`
		} `json:"data"`
	}
	targetRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.String()+"/prometheus/api/v1/targets?state=active", nil)
	if err := doctorJSON(client, targetRequest, &targets); err != nil {
		return result, fmt.Errorf("prometheus targets: %w", err)
	}
	if targets.Status != "success" || len(targets.Data.ActiveTargets) != options.ExpectedTargets {
		return result, fmt.Errorf("prometheus active targets=%d want=%d", len(targets.Data.ActiveTargets), options.ExpectedTargets)
	}
	for _, target := range targets.Data.ActiveTargets {
		if target.Health != "up" {
			return result, errors.New("prometheus has a non-up active target")
		}
		result.PrometheusTargetsUp++
	}
	return result, nil
}

func hasWildcardPermission(permissions []struct {
	Resource string   `json:"resource"`
	Actions  []string `json:"actions"`
}) bool {
	for _, permission := range permissions {
		if permission.Resource != "*" {
			continue
		}
		for _, action := range permission.Actions {
			if action == "*" {
				return true
			}
		}
	}
	return false
}

func doctorGET(ctx context.Context, client *http.Client, endpoint, authorization string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, copyErr := io.Copy(io.Discard, io.LimitReader(response.Body, maxDoctorResponseBytes))
	if copyErr != nil {
		return copyErr
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("HTTP status %d", response.StatusCode)
	}
	return nil
}

func doctorJSON(client *http.Client, request *http.Request, destination any) error {
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxDoctorResponseBytes))
		return fmt.Errorf("HTTP status %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxDoctorResponseBytes+1))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return nil
}
