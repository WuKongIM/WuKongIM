package jsonrpc

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExampleEventNotification demonstrates how to create, encode, and decode EventNotification
func ExampleEventNotification() {
	// Create a header with some flags
	header := &Header{
		NoPersist: true,
		RedDot:    false,
		SyncOnce:  true,
	}

	// Create a new EventNotification
	event := NewEventNotification("event_12345", "message_chunk", 1640995200000000000, "This is event data", header)

	// Encode the notification to JSON
	jsonData, err := Encode(event)
	if err != nil {
		fmt.Printf("Error encoding: %v\n", err)
		return
	}

	fmt.Printf("Encoded EventNotification:\n%s\n\n", string(jsonData))

	// Decode the JSON back to an EventNotification
	decoder := json.NewDecoder(strings.NewReader(string(jsonData)))
	decoded, probe, err := Decode(decoder)
	if err != nil {
		fmt.Printf("Error decoding: %v\n", err)
		return
	}

	// Type assert to EventNotification
	if eventNotif, ok := decoded.(EventNotification); ok {
		fmt.Printf("Decoded EventNotification:\n")
		fmt.Printf("  Method: %s\n", eventNotif.Method)
		fmt.Printf("  ID: %s\n", eventNotif.Params.ID)
		fmt.Printf("  Type: %s\n", eventNotif.Params.Type)
		fmt.Printf("  Timestamp: %d\n", eventNotif.Params.Timestamp)
		fmt.Printf("  Data: %s\n", eventNotif.Params.Data)
		if eventNotif.Params.Header != nil {
			fmt.Printf("  Header.NoPersist: %t\n", eventNotif.Params.Header.NoPersist)
			fmt.Printf("  Header.SyncOnce: %t\n", eventNotif.Params.Header.SyncOnce)
			fmt.Printf("  Header.End: %t\n", eventNotif.Params.Header.End)
		}
	}

	fmt.Printf("\nProbe information:\n")
	fmt.Printf("  Method: %s\n", probe.Method)
	fmt.Printf("  Has Params: %t\n", probe.Params != nil)

	// Output:
	// Encoded EventNotification:
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

// ExampleEventNotificationMinimal demonstrates creating a minimal EventNotification without header
func ExampleEventNotificationMinimal() {
	// Create a minimal EventNotification without header
	event := NewEventNotification("event_001", "minimal", 1640995200000000000, "minimal event", nil)

	// Encode to JSON
	jsonData, err := Encode(event)
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
func ExampleEventNotificationBatch() {
	// Create multiple events for the same message
	messageID := "large_message_789"
	events := []EventNotification{
		NewEventNotification(messageID+"_0", "chunk", 1640995200000000000, "First chunk of data", nil),
		NewEventNotification(messageID+"_1", "chunk", 1640995200000000001, "Second chunk of data", nil),
		NewEventNotification(messageID+"_2", "chunk_final", 1640995200000000002, "Final chunk of data", nil),
	}

	fmt.Printf("Batch of EventNotifications for message %s:\n", messageID)
	for i, event := range events {
		jsonData, err := Encode(event)
		if err != nil {
			fmt.Printf("Error encoding event %d: %v\n", i, err)
			continue
		}
		fmt.Printf("Event %d: %s\n", i, string(jsonData))
	}

	// Output:
	// Batch of EventNotifications for message large_message_789:
	// Event 0: {"jsonrpc":"2.0","method":"event","params":{"id":"large_message_789_0","type":"chunk","timestamp":1640995200000000000,"data":"First chunk of data"}}
	// Event 1: {"jsonrpc":"2.0","method":"event","params":{"id":"large_message_789_1","type":"chunk","timestamp":1640995200000000001,"data":"Second chunk of data"}}
	// Event 2: {"jsonrpc":"2.0","method":"event","params":{"id":"large_message_789_2","type":"chunk_final","timestamp":1640995200000000002,"data":"Final chunk of data"}}
}
