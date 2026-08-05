package chatlifecycle

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
)

var (
	ErrWorkerClientConfig = errors.New("chat lifecycle worker client: invalid configuration")
	ErrWorkerResponse     = errors.New("chat lifecycle worker client: invalid response")
)

// WorkerClientConfig fixes the authenticated endpoint and bounded transport.
type WorkerClientConfig struct {
	BaseURL          string
	ControlToken     string
	HTTPClient       *http.Client
	MaxResponseBytes int64
}

// WorkerClient is the typed client for the dedicated worker protocol.
type WorkerClient struct {
	baseURL     string
	token       string
	httpClient  *http.Client
	maxResponse int64
}

// NewWorkerClient validates all transport inputs before any request is sent.
func NewWorkerClient(config WorkerClientConfig) (*WorkerClient, error) {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || config.ControlToken == "" {
		return nil, ErrWorkerClientConfig
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = workerMaxResponseBytes
	}
	if config.MaxResponseBytes > workerMaxResponseBytes {
		return nil, ErrWorkerClientConfig
	}
	return &WorkerClient{
		baseURL:     strings.TrimRight(config.BaseURL, "/"),
		token:       config.ControlToken,
		httpClient:  config.HTTPClient,
		maxResponse: config.MaxResponseBytes,
	}, nil
}

func (c *WorkerClient) Health(ctx context.Context) (WorkerHealth, error) {
	var response WorkerHealth
	err := c.do(ctx, http.MethodGet, "/healthz", nil, &response)
	return response, err
}

func (c *WorkerClient) Info(ctx context.Context) (WorkerInfo, error) {
	var response WorkerInfo
	err := c.do(ctx, http.MethodGet, "/v1/info", nil, &response)
	return response, err
}

func (c *WorkerClient) Assign(ctx context.Context, assignment WorkerAssignment) (WorkerStatus, error) {
	var response WorkerStatus
	err := c.do(ctx, http.MethodPost, "/v1/chat-lifecycle/assign", assignment, &response)
	return response, err
}

func (c *WorkerClient) Start(ctx context.Context, start WorkerStartRequest) (WorkerStatus, error) {
	var response WorkerStatus
	err := c.do(ctx, http.MethodPost, "/v1/chat-lifecycle/start", start, &response)
	return response, err
}

func (c *WorkerClient) Status(ctx context.Context) (WorkerStatus, error) {
	var response WorkerStatus
	err := c.do(ctx, http.MethodGet, "/v1/chat-lifecycle/status", nil, &response)
	return response, err
}

func (c *WorkerClient) Snapshot(ctx context.Context) (WorkerSnapshot, error) {
	var response WorkerSnapshot
	err := c.do(ctx, http.MethodGet, "/v1/chat-lifecycle/snapshot", nil, &response)
	return response, err
}

func (c *WorkerClient) Checkpoint(ctx context.Context, checkpoint WorkerCheckpointRequest) (WorkerSnapshot, error) {
	var response WorkerSnapshot
	err := c.do(ctx, http.MethodPost, "/v1/chat-lifecycle/checkpoint", checkpoint, &response)
	return response, err
}

func (c *WorkerClient) UpdateRate(ctx context.Context, rate WorkerRateRequest) (WorkerStatus, error) {
	var response WorkerStatus
	err := c.do(ctx, http.MethodPost, "/v1/chat-lifecycle/rate", rate, &response)
	return response, err
}

func (c *WorkerClient) Grant(ctx context.Context, grant WorkerGrantRequest) (WorkerGrantResponse, error) {
	var response WorkerGrantResponse
	err := c.do(ctx, http.MethodPost, "/v1/chat-lifecycle/grant", grant, &response)
	return response, err
}

func (c *WorkerClient) Stop(ctx context.Context, stop WorkerStopRequest) (WorkerSnapshot, error) {
	var response WorkerSnapshot
	err := c.do(ctx, http.MethodPost, "/v1/chat-lifecycle/stop", stop, &response)
	return response, err
}

func (c *WorkerClient) do(ctx context.Context, method, path string, requestBody, responseBody any) error {
	var body io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil || int64(len(encoded)) > workerMaxRequestBytes {
			return ErrWorkerClientConfig
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("%w: request", ErrWorkerClientConfig)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctxErr := causalWorkerContextError(ctx, err); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	defer response.Body.Close()

	limited := io.LimitReader(response.Body, c.maxResponse+1)
	encoded, err := io.ReadAll(limited)
	if err != nil {
		if ctxErr := causalWorkerContextError(ctx, err); ctxErr != nil {
			return ctxErr
		}
		return ErrWorkerResponse
	}
	if int64(len(encoded)) > c.maxResponse {
		return ErrWorkerResponse
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var apiError WorkerAPIError
		if !decodeStrictWorkerResponse(encoded, &apiError) || !validWorkerErrorCode(apiError.Code) {
			return ErrWorkerResponse
		}
		apiError.Status = response.StatusCode
		return &apiError
	}
	if responseBody == nil || !decodeStrictWorkerResponse(encoded, responseBody) || !validTypedWorkerResponse(responseBody) {
		return ErrWorkerResponse
	}
	return nil
}

func causalWorkerContextError(ctx context.Context, err error) error {
	ctxErr := ctx.Err()
	if ctxErr != nil && errors.Is(err, ctxErr) {
		return ctxErr
	}
	return nil
}

func validTypedWorkerResponse(response any) bool {
	switch value := response.(type) {
	case *WorkerSnapshot:
		return validWorkerSnapshot(*value)
	case *WorkerStatus:
		return validWorkerPhase(value.Phase)
	case *WorkerHealth:
		return validWorkerPhase(value.Phase)
	case *WorkerGrantResponse:
		return validWorkerFence(value.WorkerFence) && value.WorkerCount == coordinatorWorkerCount &&
			value.WorkerID < value.WorkerCount && value.Sequence > 0
	default:
		return true
	}
}

func validWorkerPhase(phase WorkerPhase) bool {
	switch phase {
	case WorkerPhaseUnassigned, WorkerPhaseAssigned, WorkerPhaseRunning, WorkerPhaseStopping, WorkerPhaseFinal:
		return true
	default:
		return false
	}
}

func validWorkerErrorCode(code WorkerErrorCode) bool {
	switch code {
	case WorkerErrorUnauthorized, WorkerErrorNotFound, WorkerErrorMethodNotAllowed, WorkerErrorInvalidJSON,
		WorkerErrorRequestTooLarge, WorkerErrorInvalidRequest, WorkerErrorInvalidAssignment,
		WorkerErrorAssignmentConflict, WorkerErrorFenceMismatch, WorkerErrorInvalidState, WorkerErrorRuntimeFailure,
		WorkerErrorGrantGap, WorkerErrorGrantStale, WorkerErrorGrantConflict:
		return true
	default:
		return false
	}
}

func decodeStrictWorkerResponse(encoded []byte, destination any) bool {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}
