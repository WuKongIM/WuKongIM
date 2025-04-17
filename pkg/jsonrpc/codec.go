package jsonrpc

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Encode marshals the given RPC message (request, response, or notification) into JSON bytes.
func Encode(msg interface{}) ([]byte, error) {
	bytes, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("jsonrpc encode: failed to marshal message: %w", err)
	}
	return bytes, nil
}

// Decode attempts to unmarshal the JSON bytes into a known RPC message type.
// It first decodes into a temporary structure to determine the message type
// (Request, Response, Notification) based on the presence of 'id', 'method', 'result', 'error'.
// Returns the decoded message as an interface{} and an error if decoding fails
// or the message type cannot be determined.
func Decode(data []byte) (interface{}, error) {
	type Probe struct {
		Jsonrpc string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`     // Use RawMessage to check presence/absence
		Method  *string         `json:"method"` // Use pointer to check presence
		Params  json.RawMessage `json:"params"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}

	var probe Probe
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("jsonrpc decode: failed to probe message structure: %w", err)
	}

	if probe.Jsonrpc != "2.0" {
		return nil, errors.New("jsonrpc decode: invalid 'jsonrpc' version, must be \"2.0\"")
	}

	isRequest := probe.Method != nil && probe.ID != nil && string(probe.ID) != "null"
	isResponse := (probe.Result != nil || probe.Error != nil) && probe.ID != nil
	isNotification := probe.Method != nil && probe.ID == nil

	// Disambiguate between Request and Notification if ID is missing but method is present
	if probe.Method != nil && probe.ID == nil {
		isNotification = true
		isRequest = false // Cannot be a request without an ID
	} else if probe.Method == nil && probe.ID != nil { // Possible Response
		isResponse = true
		isRequest = false
		isNotification = false
	}

	switch {
	case isRequest:
		// Determine specific request type based on method
		if probe.Method == nil { // Should not happen if isRequest is true, but safety check
			return nil, errors.New("jsonrpc decode: isRequest is true but method is nil")
		}
		switch *probe.Method {
		case "connect":
			var req ConnectRequest
			if err := json.Unmarshal(data, &req); err != nil {
				return nil, fmt.Errorf("jsonrpc decode: failed to unmarshal ConnectRequest: %w", err)
			}
			return req, nil
		case "send":
			var req SendRequest
			if err := json.Unmarshal(data, &req); err != nil {
				return nil, fmt.Errorf("jsonrpc decode: failed to unmarshal SendRequest: %w", err)
			}
			return req, nil
		case "recvack":
			var req RecvAckRequest
			if err := json.Unmarshal(data, &req); err != nil {
				return nil, fmt.Errorf("jsonrpc decode: failed to unmarshal RecvAckRequest: %w", err)
			}
			return req, nil
		case "subscribe":
			var req SubscribeRequest
			if err := json.Unmarshal(data, &req); err != nil {
				return nil, fmt.Errorf("jsonrpc decode: failed to unmarshal SubscribeRequest: %w", err)
			}
			return req, nil
		case "unsubscribe":
			var req UnsubscribeRequest
			if err := json.Unmarshal(data, &req); err != nil {
				return nil, fmt.Errorf("jsonrpc decode: failed to unmarshal UnsubscribeRequest: %w", err)
			}
			return req, nil
		case "ping":
			var req PingRequest
			if err := json.Unmarshal(data, &req); err != nil {
				return nil, fmt.Errorf("jsonrpc decode: failed to unmarshal PingRequest: %w", err)
			}
			return req, nil
		case "disconnect":
			var req DisconnectRequest
			if err := json.Unmarshal(data, &req); err != nil {
				return nil, fmt.Errorf("jsonrpc decode: failed to unmarshal DisconnectRequest: %w", err)
			}
			return req, nil
		default:
			// Fallback to generic request if method is unknown?
			// Or return error? Returning error seems safer.
			return nil, fmt.Errorf("jsonrpc decode: unknown request method '%s'", *probe.Method)
		}

	case isResponse:
		// For responses, we often need the original request context (ID, method)
		// to know which specific result type to expect.
		// A simple Decode function might just return a generic response, requiring
		// the caller to unmarshal the Result field based on context.
		var resp GenericResponse // Using the helper type from types.go
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("jsonrpc decode: failed to unmarshal generic response: %w", err)
		}
		// The caller needs to inspect resp.ID and potentially unmarshal resp.Result
		// into the correct specific response type (ConnectResponse, SendResponse, etc.)
		return resp, nil

	case isNotification:
		// Determine specific notification type based on method
		if probe.Method == nil { // Should not happen if isNotification is true, but safety check
			return nil, errors.New("jsonrpc decode: isNotification is true but method is nil")
		}
		switch *probe.Method {
		case "recv":
			var notif RecvNotification
			if err := json.Unmarshal(data, &notif); err != nil {
				return nil, fmt.Errorf("jsonrpc decode: failed to unmarshal RecvNotification: %w", err)
			}
			return notif, nil
		case "disconnect": // Note: disconnect can be a request or notification
			var notif DisconnectNotification
			if err := json.Unmarshal(data, &notif); err != nil {
				return nil, fmt.Errorf("jsonrpc decode: failed to unmarshal DisconnectNotification: %w", err)
			}
			return notif, nil
		default:
			return nil, fmt.Errorf("jsonrpc decode: unknown notification method '%s'", *probe.Method)
		}

	default:
		return nil, errors.New("jsonrpc decode: unable to determine message type (not request, response, or notification)")
	}
}
