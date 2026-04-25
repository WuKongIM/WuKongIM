package jsonrpc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEventNotification_Encoding(t *testing.T) {
	// Test creating an EventNotification
	header := &Header{
		NoPersist: true,
		RedDot:    false,
	}

	event := NewEventNotification("event123", "test_type", 1640995200000000000, "event data", header)

	// Test encoding
	data, err := Encode(event)
	if err != nil {
		t.Fatalf("Failed to encode EventNotification: %v", err)
	}

	// Verify the JSON structure
	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal encoded data: %v", err)
	}

	// Check required fields
	if decoded["jsonrpc"] != "2.0" {
		t.Errorf("Expected jsonrpc '2.0', got %v", decoded["jsonrpc"])
	}

	if decoded["method"] != "event" {
		t.Errorf("Expected method 'event', got %v", decoded["method"])
	}

	params, ok := decoded["params"].(map[string]interface{})
	if !ok {
		t.Fatal("params field is not an object")
	}

	if params["id"] != "event123" {
		t.Errorf("Expected id 'event123', got %v", params["id"])
	}

	if params["type"] != "test_type" {
		t.Errorf("Expected type 'test_type', got %v", params["type"])
	}

	if params["timestamp"] != float64(1640995200000000000) { // JSON numbers are float64
		t.Errorf("Expected timestamp 1640995200000000000, got %v", params["timestamp"])
	}

	if params["data"] != "event data" {
		t.Errorf("Expected data 'event data', got %v", params["data"])
	}

	// Check header
	headerObj, ok := params["header"].(map[string]interface{})
	if !ok {
		t.Fatal("header field is not an object")
	}

	if headerObj["noPersist"] != true {
		t.Errorf("Expected header.noPersist true, got %v", headerObj["noPersist"])
	}
}

func TestEventNotification_Decoding(t *testing.T) {
	// Test JSON input
	jsonInput := `{
		"jsonrpc": "2.0",
		"method": "event",
		"params": {
			"header": {
				"noPersist": true,
				"redDot": false,
				"end": true
			},
			"id": "event456",
			"type": "test_event",
			"timestamp": 1640995200000000000,
			"data": "test event payload"
		}
	}`

	// Create decoder
	decoder := json.NewDecoder(strings.NewReader(jsonInput))

	// Decode the message
	msg, probe, err := Decode(decoder)
	if err != nil {
		t.Fatalf("Failed to decode EventNotification: %v", err)
	}

	// Verify it's an EventNotification
	event, ok := msg.(EventNotification)
	if !ok {
		t.Fatalf("Expected EventNotification, got %T", msg)
	}

	// Verify fields
	if event.Method != "event" {
		t.Errorf("Expected method 'event', got %v", event.Method)
	}

	if event.Params.ID != "event456" {
		t.Errorf("Expected id 'event456', got %v", event.Params.ID)
	}

	if event.Params.Type != "test_event" {
		t.Errorf("Expected type 'test_event', got %v", event.Params.Type)
	}

	if event.Params.Timestamp != 1640995200000000000 {
		t.Errorf("Expected timestamp 1640995200000000000, got %v", event.Params.Timestamp)
	}

	if event.Params.Data != "test event payload" {
		t.Errorf("Expected data 'test event payload', got %v", event.Params.Data)
	}

	// Verify header
	if event.Params.Header == nil {
		t.Fatal("Expected header to be present")
	}

	if !event.Params.Header.NoPersist {
		t.Errorf("Expected header.noPersist true, got %v", event.Params.Header.NoPersist)
	}

	if event.Params.Header.RedDot {
		t.Errorf("Expected header.redDot false, got %v", event.Params.Header.RedDot)
	}

	if !event.Params.Header.End {
		t.Errorf("Expected header.end true, got %v", event.Params.Header.End)
	}

	// Verify probe
	if probe.Method != "event" {
		t.Errorf("Expected probe method 'event', got %v", probe.Method)
	}
}

func TestEventNotification_RoundTrip(t *testing.T) {
	// Create original notification
	original := NewEventNotification("roundtrip123", "roundtrip_type", 1640995200000000000, "roundtrip data", nil)

	// Encode
	encoded, err := Encode(original)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Decode
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoded, _, err := Decode(decoder)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	// Verify it's the same type
	event, ok := decoded.(EventNotification)
	if !ok {
		t.Fatalf("Expected EventNotification, got %T", decoded)
	}

	// Verify all fields match
	if event.Params.ID != original.Params.ID {
		t.Errorf("ID mismatch: expected %v, got %v", original.Params.ID, event.Params.ID)
	}

	if event.Params.Type != original.Params.Type {
		t.Errorf("Type mismatch: expected %v, got %v", original.Params.Type, event.Params.Type)
	}

	if event.Params.Timestamp != original.Params.Timestamp {
		t.Errorf("Timestamp mismatch: expected %v, got %v", original.Params.Timestamp, event.Params.Timestamp)
	}

	if event.Params.Data != original.Params.Data {
		t.Errorf("Data mismatch: expected %v, got %v", original.Params.Data, event.Params.Data)
	}
}

func TestEventNotification_ValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		jsonData string
		wantErr  bool
	}{
		{
			name: "missing id",
			jsonData: `{
				"jsonrpc": "2.0",
				"method": "event",
				"params": {
					"type": "test",
					"timestamp": 1640995200000000000,
					"data": "data"
				}
			}`,
			wantErr: false, // JSON unmarshaling will succeed, but id will be empty
		},
		{
			name: "missing type",
			jsonData: `{
				"jsonrpc": "2.0",
				"method": "event",
				"params": {
					"id": "event123",
					"timestamp": 1640995200000000000,
					"data": "data"
				}
			}`,
			wantErr: false, // JSON unmarshaling will succeed, but type will be empty
		},
		{
			name: "missing data",
			jsonData: `{
				"jsonrpc": "2.0",
				"method": "event",
				"params": {
					"id": "event123",
					"type": "test",
					"timestamp": 1640995200000000000
				}
			}`,
			wantErr: false, // JSON unmarshaling will succeed, but data will be empty
		},
		{
			name: "missing params",
			jsonData: `{
				"jsonrpc": "2.0",
				"method": "event"
			}`,
			wantErr: true, // This should fail because params is required
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoder := json.NewDecoder(strings.NewReader(tt.jsonData))
			_, _, err := Decode(decoder)

			if tt.wantErr && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

func TestChunkNotification_NewFields(t *testing.T) {
	// Test the new EndReason field and End header field
	header := &Header{
		NoPersist: false,
		RedDot:    true,
		SyncOnce:  false,
		Dup:       false,
		End:       true, // New End field
	}

	// Create event with timestamp
	event := NewEventNotification("test_msg", "final_chunk", 1640995200000000000, "final chunk", header)

	// Encode
	jsonData, err := Encode(event)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Decode
	decoder := json.NewDecoder(strings.NewReader(string(jsonData)))
	decoded, _, err := Decode(decoder)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	// Verify
	eventNotif, ok := decoded.(EventNotification)
	if !ok {
		t.Fatalf("Expected EventNotification, got %T", decoded)
	}

	// Check timestamp field
	if eventNotif.Params.Timestamp != 1640995200000000000 {
		t.Errorf("Expected Timestamp 1640995200000000000, got %v", eventNotif.Params.Timestamp)
	}

	// Check End header field
	if eventNotif.Params.Header == nil {
		t.Fatal("Expected header to be present")
	}

	if !eventNotif.Params.Header.End {
		t.Errorf("Expected header.End true, got %v", eventNotif.Params.Header.End)
	}

	if !eventNotif.Params.Header.RedDot {
		t.Errorf("Expected header.RedDot true, got %v", eventNotif.Params.Header.RedDot)
	}
}
