package jsonrpc

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExampleNewFields demonstrates the new EndReason and End fields
func ExampleNewFields() {
	fmt.Println("=== Demonstrating New Fields in ChunkNotification ===\n")

	// Example 1: Regular chunk with no end reason
	fmt.Println("1. Regular chunk (not final):")
	regularHeader := &Header{
		NoPersist: false,
		RedDot:    true,
		SyncOnce:  false,
		Dup:       false,
		End:       false, // Not the end
	}
	
	regularChunk := NewChunkNotification("msg_001", 0, 0, "First chunk of data", regularHeader)
	regularJSON, _ := Encode(regularChunk)
	fmt.Printf("   JSON: %s\n\n", string(regularJSON))

	// Example 2: Final chunk with end reason
	fmt.Println("2. Final chunk (with end reason):")
	finalHeader := &Header{
		NoPersist: true,
		RedDot:    false,
		SyncOnce:  true,
		Dup:       false,
		End:       true, // This is the end
	}
	
	finalChunk := NewChunkNotification("msg_001", 5, 1, "Final chunk of data", finalHeader)
	finalJSON, _ := Encode(finalChunk)
	fmt.Printf("   JSON: %s\n\n", string(finalJSON))

	// Example 3: Error chunk with specific end reason
	fmt.Println("3. Error chunk (with error end reason):")
	errorHeader := &Header{
		NoPersist: false,
		RedDot:    true,
		SyncOnce:  false,
		Dup:       true, // Duplicate due to error
		End:       true, // Ending due to error
	}
	
	errorChunk := NewChunkNotification("msg_002", 2, 500, "Error occurred", errorHeader)
	errorJSON, _ := Encode(errorChunk)
	fmt.Printf("   JSON: %s\n\n", string(errorJSON))

	// Example 4: Decoding and inspecting the new fields
	fmt.Println("4. Decoding and inspecting new fields:")
	decoder := json.NewDecoder(strings.NewReader(string(finalJSON)))
	decoded, _, err := Decode(decoder)
	if err != nil {
		fmt.Printf("   Error: %v\n", err)
		return
	}

	if chunk, ok := decoded.(ChunkNotification); ok {
		fmt.Printf("   MessageID: %s\n", chunk.Params.MessageID)
		fmt.Printf("   ChunkID: %d\n", chunk.Params.ChunkID)
		fmt.Printf("   EndReason: %d\n", chunk.Params.EndReason)
		fmt.Printf("   Payload: %s\n", chunk.Params.Payload)
		
		if chunk.Params.Header != nil {
			fmt.Printf("   Header.End: %t\n", chunk.Params.Header.End)
			fmt.Printf("   Header.NoPersist: %t\n", chunk.Params.Header.NoPersist)
			fmt.Printf("   Header.SyncOnce: %t\n", chunk.Params.Header.SyncOnce)
		}
	}

	fmt.Println("\n=== End Reason Codes (Example) ===")
	fmt.Println("0: Normal continuation")
	fmt.Println("1: Normal completion")
	fmt.Println("2: Timeout")
	fmt.Println("3: Cancelled")
	fmt.Println("4: Size limit exceeded")
	fmt.Println("500: Internal error")
}

// ExampleEndReasonUsage shows different end reason scenarios
func ExampleEndReasonUsage() {
	fmt.Println("=== End Reason Usage Scenarios ===\n")

	scenarios := []struct {
		name      string
		endReason int
		isEnd     bool
		payload   string
	}{
		{"Continuing", 0, false, "More data coming..."},
		{"Normal End", 1, true, "Transfer complete"},
		{"Timeout", 2, true, "Transfer timed out"},
		{"Cancelled", 3, true, "Transfer cancelled by user"},
		{"Size Limit", 4, true, "File too large"},
		{"Error", 500, true, "Internal server error"},
	}

	for i, scenario := range scenarios {
		header := &Header{End: scenario.isEnd}
		chunk := NewChunkNotification("demo_msg", i, scenario.endReason, scenario.payload, header)
		
		jsonData, _ := Encode(chunk)
		fmt.Printf("%s (EndReason: %d, End: %t):\n", scenario.name, scenario.endReason, scenario.isEnd)
		fmt.Printf("  %s\n\n", string(jsonData))
	}
}

// ExampleBackwardCompatibility demonstrates that existing code still works
func ExampleBackwardCompatibility() {
	fmt.Println("=== Backward Compatibility Test ===\n")

	// Old-style JSON without the new fields should still decode properly
	oldStyleJSON := `{
		"jsonrpc": "2.0",
		"method": "chunk",
		"params": {
			"messageId": "legacy_msg",
			"chunkId": 1,
			"payload": "legacy data"
		}
	}`

	decoder := json.NewDecoder(strings.NewReader(oldStyleJSON))
	decoded, _, err := Decode(decoder)
	if err != nil {
		fmt.Printf("Error decoding legacy JSON: %v\n", err)
		return
	}

	if chunk, ok := decoded.(ChunkNotification); ok {
		fmt.Printf("Successfully decoded legacy JSON:\n")
		fmt.Printf("  MessageID: %s\n", chunk.Params.MessageID)
		fmt.Printf("  ChunkID: %d\n", chunk.Params.ChunkID)
		fmt.Printf("  EndReason: %d (default)\n", chunk.Params.EndReason)
		fmt.Printf("  Payload: %s\n", chunk.Params.Payload)
		fmt.Printf("  Header: %v (nil is expected for legacy)\n", chunk.Params.Header)
	}

	fmt.Println("\nBackward compatibility maintained! ✓")
}
