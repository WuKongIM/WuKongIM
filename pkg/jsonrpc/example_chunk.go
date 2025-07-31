package jsonrpc

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExampleChunkNotification demonstrates how to create, encode, and decode ChunkNotification
func ExampleChunkNotification() {
	// Create a header with some flags
	header := &Header{
		NoPersist: true,
		RedDot:    false,
		SyncOnce:  true,
	}

	// Create a new ChunkNotification
	chunk := NewChunkNotification("message_12345", 3, 0, "This is chunk data", header)

	// Encode the notification to JSON
	jsonData, err := Encode(chunk)
	if err != nil {
		fmt.Printf("Error encoding: %v\n", err)
		return
	}

	fmt.Printf("Encoded ChunkNotification:\n%s\n\n", string(jsonData))

	// Decode the JSON back to a ChunkNotification
	decoder := json.NewDecoder(strings.NewReader(string(jsonData)))
	decoded, probe, err := Decode(decoder)
	if err != nil {
		fmt.Printf("Error decoding: %v\n", err)
		return
	}

	// Type assert to ChunkNotification
	if chunkNotif, ok := decoded.(ChunkNotification); ok {
		fmt.Printf("Decoded ChunkNotification:\n")
		fmt.Printf("  Method: %s\n", chunkNotif.Method)
		fmt.Printf("  MessageID: %s\n", chunkNotif.Params.MessageID)
		fmt.Printf("  ChunkID: %d\n", chunkNotif.Params.ChunkID)
		fmt.Printf("  EndReason: %d\n", chunkNotif.Params.EndReason)
		fmt.Printf("  Payload: %s\n", chunkNotif.Params.Payload)
		if chunkNotif.Params.Header != nil {
			fmt.Printf("  Header.NoPersist: %t\n", chunkNotif.Params.Header.NoPersist)
			fmt.Printf("  Header.SyncOnce: %t\n", chunkNotif.Params.Header.SyncOnce)
			fmt.Printf("  Header.End: %t\n", chunkNotif.Params.Header.End)
		}
	}

	fmt.Printf("\nProbe information:\n")
	fmt.Printf("  Method: %s\n", probe.Method)
	fmt.Printf("  Has Params: %t\n", probe.Params != nil)

	// Output:
	// Encoded ChunkNotification:
	// {"jsonrpc":"2.0","method":"chunk","params":{"header":{"noPersist":true,"syncOnce":true},"messageId":"message_12345","chunkId":3,"payload":"This is chunk data"}}
	//
	// Decoded ChunkNotification:
	//   Method: chunk
	//   MessageID: message_12345
	//   ChunkID: 3
	//   Payload: This is chunk data
	//   Header.NoPersist: true
	//   Header.SyncOnce: true
	//
	// Probe information:
	//   Method: chunk
	//   Has Params: true
}

// ExampleChunkNotificationMinimal demonstrates creating a minimal ChunkNotification without header
func ExampleChunkNotificationMinimal() {
	// Create a minimal ChunkNotification without header
	chunk := NewChunkNotification("msg_001", 1, 0, "minimal chunk", nil)

	// Encode to JSON
	jsonData, err := Encode(chunk)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Minimal ChunkNotification:\n%s\n", string(jsonData))

	// Output:
	// Minimal ChunkNotification:
	// {"jsonrpc":"2.0","method":"chunk","params":{"messageId":"msg_001","chunkId":1,"payload":"minimal chunk"}}
}

// ExampleChunkNotificationBatch demonstrates handling multiple chunk notifications
func ExampleChunkNotificationBatch() {
	// Create multiple chunks for the same message
	messageID := "large_message_789"
	chunks := []ChunkNotification{
		NewChunkNotification(messageID, 0, 0, "First chunk of data", nil),
		NewChunkNotification(messageID, 1, 0, "Second chunk of data", nil),
		NewChunkNotification(messageID, 2, 1, "Final chunk of data", nil), // End reason 1 for final chunk
	}

	fmt.Printf("Batch of ChunkNotifications for message %s:\n", messageID)
	for i, chunk := range chunks {
		jsonData, err := Encode(chunk)
		if err != nil {
			fmt.Printf("Error encoding chunk %d: %v\n", i, err)
			continue
		}
		fmt.Printf("Chunk %d: %s\n", i, string(jsonData))
	}

	// Output:
	// Batch of ChunkNotifications for message large_message_789:
	// Chunk 0: {"jsonrpc":"2.0","method":"chunk","params":{"messageId":"large_message_789","chunkId":0,"payload":"First chunk of data"}}
	// Chunk 1: {"jsonrpc":"2.0","method":"chunk","params":{"messageId":"large_message_789","chunkId":1,"payload":"Second chunk of data"}}
	// Chunk 2: {"jsonrpc":"2.0","method":"chunk","params":{"messageId":"large_message_789","chunkId":2,"payload":"Final chunk of data"}}
}
