package jsonrpc

import (
	"encoding/json"
	"errors"
	"fmt"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	// Ensure io is imported
)

// JSON-RPC Version
const jsonRPCVersion = "2.0"

// JSON-RPC Method Names
const (
	MethodConnect     = "connect"
	MethodSend        = "send"
	MethodRecvAck     = "recvack"
	MethodSubscribe   = "subscribe"
	MethodUnsubscribe = "unsubscribe"
	MethodPing        = "ping"
	MethodPong        = "pong"
	MethodDisconnect  = "disconnect"
	MethodRecv        = "recv"  // Notification method
	MethodEvent       = "event" // Event notification method
)

// Predefined decoding errors
var (
	ErrInvalidVersion       = errors.New("jsonrpc: invalid version")
	ErrInvalidStructure     = errors.New("jsonrpc: invalid message structure or field combination")
	ErrAmbiguousMessageType = errors.New("jsonrpc: ambiguous message type")
	ErrResponseFormat       = errors.New("jsonrpc: invalid response format (missing id, result/error mismatch)")
	ErrRequestFormat        = errors.New("jsonrpc: invalid request format (missing id or method)")
	ErrNotificationFormat   = errors.New("jsonrpc: invalid notification format (missing method)")
	ErrUnknownMethod        = errors.New("jsonrpc: unknown method")
	ErrMissingParams        = errors.New("jsonrpc: missing params field")
	ErrUnmarshalFieldFailed = errors.New("jsonrpc: failed to unmarshal field") // Base error for wrapping
	ErrInternal             = errors.New("jsonrpc: internal codec error")      // For unexpected logic paths
)

// Probe is a temporary structure used to determine the type of an incoming JSON-RPC message
// by checking the presence of key fields like id, method, result, error.
type Probe struct {
	Jsonrpc json.RawMessage `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

const (
	msgTypeUnknown int = iota
	msgTypeRequest
	msgTypeResponse
	msgTypeNotification
)

// decodingError creates a formatted error specific to JSON-RPC decoding.
// It's a simple wrapper around fmt.Errorf with a prefix.
// Callers should use %w in the format string to wrap base errors when needed.
func decodingError(format string, args ...interface{}) error {
	return fmt.Errorf("jsonrpc decode: "+format, args...)
}

// Encode marshals the given RPC message (request, response, or notification) into JSON bytes.
func Encode(msg interface{}) ([]byte, error) {
	bytes, err := json.Marshal(msg)
	if err != nil {
		// Use a consistent error format if desired, though encode errors are simpler
		return nil, fmt.Errorf("jsonrpc encode: failed to marshal message: %w", err)
	}
	return bytes, nil
}

func EncodeErrorResponse(id string, err error) []byte {
	data, err := Encode(GenericResponse{
		BaseResponse: BaseResponse{
			Jsonrpc: jsonRPCVersion,
			ID:      id,
			Error: &ErrorObject{
				Message: err.Error(),
			},
		},
	})
	if err != nil {
		return nil
	}
	return data
}

func DecodeID(id json.RawMessage) string {
	var idStr string
	if err := json.Unmarshal(id, &idStr); err != nil {
		return ""
	}
	return idStr
}

// determineMessageType probes the basic structure, validates version and fields,
// and determines if the message is a Request, Response, or Notification.
func determineMessageType(probe *Probe) (msgType int, version string, err error) {
	// Validate jsonrpc version (default to 2.0)
	version = jsonRPCVersion
	if probe.Jsonrpc != nil {
		var parsedVersion string
		if jsonErr := json.Unmarshal(probe.Jsonrpc, &parsedVersion); jsonErr != nil {
			err = fmt.Errorf("%w: jsonrpc field: %w", ErrUnmarshalFieldFailed, jsonErr) // Wrap base error
			return
		}
		if parsedVersion != jsonRPCVersion {
			err = fmt.Errorf("%w: expected '%s', got '%s'", ErrInvalidVersion, jsonRPCVersion, parsedVersion)
			return
		}
		version = parsedVersion
	}

	// Check field presence
	idIsNull := probe.ID == nil || string(probe.ID) == ""
	idIsPresent := probe.ID != nil && string(probe.ID) != ""
	methodIsPresent := probe.Method != ""
	resultIsPresent := probe.Result != nil
	errorIsPresent := probe.Error != nil

	// Determine type PRELIMINARILY
	// Note: We refine this based on validation checks later
	prelimIsNotification := methodIsPresent && (!idIsPresent || idIsNull)
	prelimIsResponse := (idIsPresent && !idIsNull && !methodIsPresent && (resultIsPresent || errorIsPresent))
	prelimIsRequest := methodIsPresent && idIsPresent && !idIsNull

	// Validate field combinations
	switch {
	case prelimIsRequest && prelimIsResponse:
		err = ErrInvalidStructure // Use predefined error
		return
	case prelimIsResponse && !resultIsPresent && !errorIsPresent:
		err = ErrResponseFormat // Use predefined error
		return
	case prelimIsResponse && resultIsPresent && errorIsPresent:
		err = ErrResponseFormat // Use predefined error
		return
		// case prelimIsRequest && prelimIsNotification: // This overlap is handled by specific type assignment below
		//	 err = decodingError("message ambiguity: matches request and notification criteria (id: %s, method: %v)", string(probe.ID), probe.Method)
		//	 return
	}

	// Assign FINAL message type based on valid combinations
	if prelimIsRequest {
		// Valid request: method, non-null id
		msgType = msgTypeRequest
	} else if prelimIsResponse {
		// Valid response: non-null id, no method, result or error
		msgType = msgTypeResponse
	} else if prelimIsNotification {
		// Valid notification: method, no id or null id
		// Check if method is a known notification type (optional, depending on strictness)
		switch probe.Method {
		case MethodRecv, MethodDisconnect, MethodRecvAck, MethodEvent:
			msgType = msgTypeNotification
		default:
			// If method is present but ID is missing/null, AND method is not known,
			// treat as invalid/unknown type according to stricter interpretation.
			// This will lead to the 'unable to determine' error below.
			msgType = msgTypeUnknown
			err = decodingError("unknown notification method '%s'", probe.Method)
			// We set the type to Unknown here, the caller might still get the specific error.
			// Let's refine: if we identify it as a notification structure but unknown method,
			// should we error here or let the main Decode switch handle it?
			// Let main switch handle unknown method for cleaner separation.
			msgType = msgTypeNotification // Still structurally a notification

		}
	} else if methodIsPresent && (!idIsPresent || idIsNull) {
		// This covers the case where prelimIsNotification was false (e.g. unknown method) but structure fits notification
		// Re-evaluate based on stricter need? No, the logic above handles known notifications.
		// If it's not Request/Response/Known Notification, it's unknown.
		msgType = msgTypeUnknown

	} else {
		// Catch-all for other invalid combinations (e.g., only id, only result)
		msgType = msgTypeUnknown
		err = decodingError("unable to determine message type (invalid field combination) method: %s, id: %s, result: %s, error: %s", probe.Method, string(probe.ID), string(probe.Result), string(probe.Error))
	}

	if msgType == msgTypeUnknown && err == nil { // Assign error if type is unknown and no specific validation failed
		err = ErrInvalidStructure // Assign general structure error if type unknown
	}

	return
}

// Decode reads and decodes a single JSON-RPC message.
// It returns the decoded message as an interface{}, the intermediate Probe struct,
// and an error if decoding fails.
func Decode(decoder *json.Decoder) (interface{}, Probe, error) {

	// 1. Probe the message structure
	var probe Probe
	if err := decoder.Decode(&probe); err != nil {
		// If it's a json syntax error, wrap it?
		var syntaxErr *json.SyntaxError
		if errors.As(err, &syntaxErr) {
			return nil, probe, fmt.Errorf("%w: %w", ErrInvalidStructure, err)
		}
		return nil, probe, err // Includes io.EOF
	}

	// 2. Determine message type and validate basic structure
	msgType, version, err := determineMessageType(&probe)
	if err != nil {
		return nil, probe, err // Return error from determination (already specific)
	}

	// 3. Construct and Populate the Specific Type based on msgType
	switch msgType {
	case msgTypeRequest:
		if probe.Method == "" { // Should be caught by determineMessageType
			return nil, probe, ErrRequestFormat
		}
		baseReq := BaseRequest{Jsonrpc: version, Method: probe.Method}
		if probe.ID == nil || string(probe.ID) == "null" { // ID is mandatory and non-null for requests
			return nil, probe, ErrRequestFormat
		}
		if err := json.Unmarshal(probe.ID, &baseReq.ID); err != nil {
			return nil, probe, fmt.Errorf("%w: request ID: %w", ErrUnmarshalFieldFailed, err)
		}

		switch probe.Method {
		case MethodConnect:
			var req ConnectRequest
			req.BaseRequest = baseReq
			if probe.Params == nil {
				return nil, probe, fmt.Errorf("%w: method %s", ErrMissingParams, MethodConnect)
			}
			if err := json.Unmarshal(probe.Params, &req.Params); err != nil {
				return nil, probe, fmt.Errorf("%w: %s params: %w", ErrUnmarshalFieldFailed, MethodConnect, err)
			}
			return req, probe, nil
		case MethodSend:
			var req SendRequest
			req.BaseRequest = baseReq
			if probe.Params == nil {
				return nil, probe, fmt.Errorf("%w: method %s", ErrMissingParams, MethodSend)
			}
			if err := json.Unmarshal(probe.Params, &req.Params); err != nil {
				return nil, probe, fmt.Errorf("%w: %s params: %w", ErrUnmarshalFieldFailed, MethodSend, err)
			}
			return req, probe, nil
		case MethodSubscribe:
			var req SubscribeRequest
			req.BaseRequest = baseReq
			if probe.Params == nil {
				return nil, probe, fmt.Errorf("%w: method %s", ErrMissingParams, MethodSubscribe)
			}
			if err := json.Unmarshal(probe.Params, &req.Params); err != nil {
				return nil, probe, fmt.Errorf("%w: %s params: %w", ErrUnmarshalFieldFailed, MethodSubscribe, err)
			}
			return req, probe, nil
		case MethodUnsubscribe:
			var req UnsubscribeRequest
			req.BaseRequest = baseReq
			if probe.Params == nil {
				return nil, probe, fmt.Errorf("%w: method %s", ErrMissingParams, MethodUnsubscribe)
			}
			if err := json.Unmarshal(probe.Params, &req.Params); err != nil {
				return nil, probe, fmt.Errorf("%w: %s params: %w", ErrUnmarshalFieldFailed, MethodUnsubscribe, err)
			}
			return req, probe, nil
		case MethodPing:
			var req PingRequest
			req.BaseRequest = baseReq
			if probe.Params != nil && string(probe.Params) != "null" {
				var p PingParams
				if err := json.Unmarshal(probe.Params, &p); err != nil {
					if string(probe.Params) != "{}" { // Allow empty object
						return nil, probe, fmt.Errorf("%w: %s params: %w", ErrUnmarshalFieldFailed, MethodPing, err)
					}
				}
				req.Params = &p
			}
			return req, probe, nil
		case MethodDisconnect:
			var req DisconnectRequest
			req.BaseRequest = baseReq
			if probe.Params == nil {
				return nil, probe, fmt.Errorf("%w: method %s", ErrMissingParams, MethodDisconnect)
			}
			if err := json.Unmarshal(probe.Params, &req.Params); err != nil {
				return nil, probe, fmt.Errorf("%w: %s params: %w", ErrUnmarshalFieldFailed, MethodDisconnect, err)
			}
			return req, probe, nil
		default:
			return nil, probe, fmt.Errorf("%w: %s", ErrUnknownMethod, probe.Method)
		}

	case msgTypeResponse:
		baseResp := BaseResponse{Jsonrpc: version}
		if probe.ID == nil || string(probe.ID) == "null" { // ID is mandatory and non-null for responses
			return nil, probe, ErrResponseFormat
		}
		if err := json.Unmarshal(probe.ID, &baseResp.ID); err != nil {
			return nil, probe, fmt.Errorf("%w: response ID: %w", ErrUnmarshalFieldFailed, err)
		}
		if probe.Result == nil && probe.Error == nil {
			return nil, probe, ErrResponseFormat // Must have result or error
		}
		if probe.Result != nil && probe.Error != nil {
			return nil, probe, ErrResponseFormat // Cannot have both
		}

		resp := GenericResponse{
			BaseResponse: baseResp,
			Result:       probe.Result,
		}
		if probe.Error != nil {
			var errObj ErrorObject
			if err := json.Unmarshal(probe.Error, &errObj); err != nil {
				return nil, probe, fmt.Errorf("%w: error object: %w", ErrUnmarshalFieldFailed, err)
			}
			resp.Error = &errObj
		}
		return resp, probe, nil

	case msgTypeNotification:
		if probe.Method == "" { // Should be caught by determineMessageType
			return nil, probe, ErrNotificationFormat
		}
		baseNotif := BaseNotification{Jsonrpc: version, Method: probe.Method}

		switch probe.Method {
		case MethodRecv:
			var notif RecvNotification
			notif.BaseNotification = baseNotif
			if probe.Params == nil {
				return nil, probe, fmt.Errorf("%w: method %s", ErrMissingParams, MethodRecv)
			}
			if err := json.Unmarshal(probe.Params, &notif.Params); err != nil {
				return nil, probe, fmt.Errorf("%w: %s params: %w", ErrUnmarshalFieldFailed, MethodRecv, err)
			}
			return notif, probe, nil
		case MethodRecvAck:
			var notif RecvAckNotification
			notif.BaseNotification = baseNotif
			if probe.Params == nil {
				return nil, probe, fmt.Errorf("%w: method %s", ErrMissingParams, MethodRecvAck)
			}
			if err := json.Unmarshal(probe.Params, &notif.Params); err != nil {
				return nil, probe, fmt.Errorf("%w: %s params: %w", ErrUnmarshalFieldFailed, MethodRecvAck, err)
			}
			return notif, probe, nil
		case MethodDisconnect:
			var notif DisconnectNotification
			notif.BaseNotification = baseNotif
			if probe.Params == nil {
				return nil, probe, fmt.Errorf("%w: method %s", ErrMissingParams, MethodDisconnect)
			}
			if err := json.Unmarshal(probe.Params, &notif.Params); err != nil {
				return nil, probe, fmt.Errorf("%w: %s params: %w", ErrUnmarshalFieldFailed, MethodDisconnect, err)
			}
			return notif, probe, nil
		case MethodEvent:
			var notif EventNotification
			notif.BaseNotification = baseNotif
			if probe.Params == nil {
				return nil, probe, fmt.Errorf("%w: method %s", ErrMissingParams, MethodEvent)
			}
			if err := json.Unmarshal(probe.Params, &notif.Params); err != nil {
				return nil, probe, fmt.Errorf("%w: %s params: %w", ErrUnmarshalFieldFailed, MethodEvent, err)
			}
			return notif, probe, nil
		default:
			return nil, probe, fmt.Errorf("%w: %s", ErrUnknownMethod, probe.Method)
		}

	default: // msgTypeUnknown or other unexpected case
		// If determineMessageType returned an error, it's already specific.
		// Otherwise, return the general invalid structure error.
		if err == nil {
			err = ErrInvalidStructure
		}
		return nil, probe, err
	}
}

func ToFrame(packet interface{}) (wkproto.Frame, string, error) {

	switch p := packet.(type) {
	case ConnectRequest:
		return p.Params.ToProto(), p.ID, nil
	case SendRequest:
		return p.Params.ToProto(), p.ID, nil
	case PingRequest:
		return &wkproto.PingPacket{}, p.ID, nil
	case DisconnectRequest:
		return p.Params.ToProto(), p.ID, nil
	case RecvAckNotification:
		return p.Params.ToProto(), "", nil
	}
	return nil, "", fmt.Errorf("unknown packet type: %T", packet)
}

func FromFrame(reqId string, frame wkproto.Frame) (interface{}, error) {
	switch frame.GetFrameType() {
	case wkproto.CONNACK:
		connack := frame.(*wkproto.ConnackPacket)
		params := FromProtoConnectAck(connack)
		return ConnectResponse{
			BaseResponse: BaseResponse{
				Jsonrpc: jsonRPCVersion,
				ID:      reqId,
			},
			Result: params,
		}, nil
	case wkproto.SENDACK:
		sendack := frame.(*wkproto.SendackPacket)
		result := FromProtoSendAck(sendack)
		return SendResponse{
			BaseResponse: BaseResponse{
				Jsonrpc: jsonRPCVersion,
				ID:      reqId,
			},
			Result: result,
		}, nil
	case wkproto.RECV:
		recv := frame.(*wkproto.RecvPacket)
		result := FromProtoRecvNotification(recv)
		return result, nil
	case wkproto.EVENT:
		event := frame.(*wkproto.EventPacket)
		result := FromProtoEventNotification(event)
		return result, nil
	case wkproto.DISCONNECT:
		disconnect := frame.(*wkproto.DisconnectPacket)
		params := FromProtoDisconnectPacket(disconnect)
		return DisconnectNotification{
			BaseNotification: BaseNotification{
				Jsonrpc: jsonRPCVersion,
				Method:  MethodDisconnect,
			},
			Params: params,
		}, nil
	case wkproto.PONG:
		return PongResponse{
			BaseResponse: BaseResponse{
				Jsonrpc: jsonRPCVersion,
				ID:      reqId,
			},
		}, nil
	}
	return nil, fmt.Errorf("jsonrpc: unknown frame type: %d", frame.GetFrameType())
}

// IsJSONObjectPrefix checks if the byte slice likely starts with a JSON object,
// ignoring leading whitespace. It only checks for the opening curly brace '{'.
// It does NOT validate the entire JSON object.
func IsJSONObjectPrefix(data []byte) bool {
	// Iterate through the data, skipping leading whitespace characters.
	// JSON whitespace characters are space (U+0020), tab (U+0009),
	// line feed (U+000A), and carriage return (U+000D).
	for _, b := range data {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue // Skip whitespace
		case '{':
			// Found the opening brace after skipping whitespace.
			return true
		default:
			// Found a non-whitespace character that is not '{'.
			return false
		}
	}

	// If we reach here, the input was either empty or contained only whitespace.
	return false
}
