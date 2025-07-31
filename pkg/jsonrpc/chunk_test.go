package jsonrpc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChunkNotification_Encoding(t *testing.T) {
	// Test creating a ChunkNotification
	header := &Header{
		NoPersist: true,
		RedDot:    false,
	}

	chunk := NewChunkNotification("msg123", 1, 0, "chunk data", header)

	// Test encoding
	data, err := Encode(chunk)
	if err != nil {
		t.Fatalf("Failed to encode ChunkNotification: %v", err)
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

	if decoded["method"] != "chunk" {
		t.Errorf("Expected method 'chunk', got %v", decoded["method"])
	}

	params, ok := decoded["params"].(map[string]interface{})
	if !ok {
		t.Fatal("params field is not an object")
	}

	if params["messageId"] != "msg123" {
		t.Errorf("Expected messageId 'msg123', got %v", params["messageId"])
	}

	if params["chunkId"] != float64(1) { // JSON numbers are float64
		t.Errorf("Expected chunkId 1, got %v", params["chunkId"])
	}

	if params["endReason"] != float64(0) { // JSON numbers are float64
		t.Errorf("Expected endReason 0, got %v", params["endReason"])
	}

	if params["payload"] != "chunk data" {
		t.Errorf("Expected payload 'chunk data', got %v", params["payload"])
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

func TestChunkNotification_Decoding(t *testing.T) {
	// Test JSON input
	jsonInput := `{
		"jsonrpc": "2.0",
		"method": "chunk",
		"params": {
			"header": {
				"noPersist": true,
				"redDot": false,
				"end": true
			},
			"messageId": "msg456",
			"chunkId": 2,
			"endReason": 5,
			"payload": "test chunk payload"
		}
	}`

	// Create decoder
	decoder := json.NewDecoder(strings.NewReader(jsonInput))

	// Decode the message
	msg, probe, err := Decode(decoder)
	if err != nil {
		t.Fatalf("Failed to decode ChunkNotification: %v", err)
	}

	// Verify it's a ChunkNotification
	chunk, ok := msg.(ChunkNotification)
	if !ok {
		t.Fatalf("Expected ChunkNotification, got %T", msg)
	}

	// Verify fields
	if chunk.Method != "chunk" {
		t.Errorf("Expected method 'chunk', got %v", chunk.Method)
	}

	if chunk.Params.MessageID != "msg456" {
		t.Errorf("Expected messageId 'msg456', got %v", chunk.Params.MessageID)
	}

	if chunk.Params.ChunkID != 2 {
		t.Errorf("Expected chunkId 2, got %v", chunk.Params.ChunkID)
	}

	if chunk.Params.EndReason != 5 {
		t.Errorf("Expected endReason 5, got %v", chunk.Params.EndReason)
	}

	if chunk.Params.Payload != "test chunk payload" {
		t.Errorf("Expected payload 'test chunk payload', got %v", chunk.Params.Payload)
	}

	// Verify header
	if chunk.Params.Header == nil {
		t.Fatal("Expected header to be present")
	}

	if !chunk.Params.Header.NoPersist {
		t.Errorf("Expected header.noPersist true, got %v", chunk.Params.Header.NoPersist)
	}

	if chunk.Params.Header.RedDot {
		t.Errorf("Expected header.redDot false, got %v", chunk.Params.Header.RedDot)
	}

	if !chunk.Params.Header.End {
		t.Errorf("Expected header.end true, got %v", chunk.Params.Header.End)
	}

	// Verify probe
	if probe.Method != "chunk" {
		t.Errorf("Expected probe method 'chunk', got %v", probe.Method)
	}
}

func TestChunkNotification_RoundTrip(t *testing.T) {
	// Create original notification
	original := NewChunkNotification("roundtrip123", 5, 0, "roundtrip data", nil)

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
	chunk, ok := decoded.(ChunkNotification)
	if !ok {
		t.Fatalf("Expected ChunkNotification, got %T", decoded)
	}

	// Verify all fields match
	if chunk.Params.MessageID != original.Params.MessageID {
		t.Errorf("MessageID mismatch: expected %v, got %v", original.Params.MessageID, chunk.Params.MessageID)
	}

	if chunk.Params.ChunkID != original.Params.ChunkID {
		t.Errorf("ChunkID mismatch: expected %v, got %v", original.Params.ChunkID, chunk.Params.ChunkID)
	}

	if chunk.Params.EndReason != original.Params.EndReason {
		t.Errorf("EndReason mismatch: expected %v, got %v", original.Params.EndReason, chunk.Params.EndReason)
	}

	if chunk.Params.Payload != original.Params.Payload {
		t.Errorf("Payload mismatch: expected %v, got %v", original.Params.Payload, chunk.Params.Payload)
	}
}

func TestChunkNotification_ValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		jsonData string
		wantErr  bool
	}{
		{
			name: "missing messageId",
			jsonData: `{
				"jsonrpc": "2.0",
				"method": "chunk",
				"params": {
					"chunkId": 1,
					"payload": "data"
				}
			}`,
			wantErr: false, // JSON unmarshaling will succeed, but messageId will be empty
		},
		{
			name: "missing chunkId",
			jsonData: `{
				"jsonrpc": "2.0",
				"method": "chunk",
				"params": {
					"messageId": "msg123",
					"payload": "data"
				}
			}`,
			wantErr: false, // JSON unmarshaling will succeed, but chunkId will be 0
		},
		{
			name: "missing payload",
			jsonData: `{
				"jsonrpc": "2.0",
				"method": "chunk",
				"params": {
					"messageId": "msg123",
					"chunkId": 1
				}
			}`,
			wantErr: false, // JSON unmarshaling will succeed, but payload will be empty
		},
		{
			name: "missing params",
			jsonData: `{
				"jsonrpc": "2.0",
				"method": "chunk"
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

	// Create chunk with EndReason
	chunk := NewChunkNotification("test_msg", 3, 42, "final chunk", header)

	// Encode
	jsonData, err := Encode(chunk)
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
	chunkNotif, ok := decoded.(ChunkNotification)
	if !ok {
		t.Fatalf("Expected ChunkNotification, got %T", decoded)
	}

	// Check EndReason field
	if chunkNotif.Params.EndReason != 42 {
		t.Errorf("Expected EndReason 42, got %v", chunkNotif.Params.EndReason)
	}

	// Check End header field
	if chunkNotif.Params.Header == nil {
		t.Fatal("Expected header to be present")
	}

	if !chunkNotif.Params.Header.End {
		t.Errorf("Expected header.End true, got %v", chunkNotif.Params.Header.End)
	}

	if !chunkNotif.Params.Header.RedDot {
		t.Errorf("Expected header.RedDot true, got %v", chunkNotif.Params.Header.RedDot)
	}
}
