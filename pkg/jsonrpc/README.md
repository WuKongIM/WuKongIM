# JSON-RPC 2.0 Codec Package (`pkg/jsonrpc`)

## Overview

This package provides utilities for encoding and decoding messages according to the JSON-RPC 2.0 specification. It is designed to handle the specific request, response, and notification types used within the WuKongIM system, defined in `types.go`.

## Key Functions

### `Encode(msg interface{}) ([]byte, error)`

Takes a Go struct representing a JSON-RPC message (Request, Response, or Notification, e.g., `ConnectRequest`, `GenericResponse`) and marshals it into a JSON byte slice. It relies on standard `json.Marshal` behavior and the `json:` tags defined on the message structs.

**Example:**

```go
import "github.com/WuKongIM/WuKongIM/pkg/jsonrpc"

// Assuming connectReq is a populated jsonrpc.ConnectRequest struct
jsonData, err := jsonrpc.Encode(connectReq)
if err != nil {
    // Handle encoding error
}
// jsonData now holds the JSON-RPC request bytes
```

### `Decode(decoder *json.Decoder) (interface{}, Probe, error)`

Reads the next JSON object from the provided `json.Decoder` (which typically wraps an `io.Reader` like a network connection) and attempts to decode it into one of the recognized WuKongIM JSON-RPC message types.

1.  **Probing:** It first decodes the message into a temporary `Probe` struct to determine the message type (Request, Response, or Notification) based on the presence of fields like `id`, `method`, `result`, and `error`.
2.  **Type Validation:** It validates the basic structure against JSON-RPC 2.0 rules.
3.  **Specific Decoding:** Based on the determined type and the `method` field (for requests/notifications), it decodes the message into the corresponding specific Go struct (e.g., `ConnectRequest`, `SendRequest`, `GenericResponse`, `RecvNotification`).
4.  **Return Values:**
    *   `interface{}`: The decoded message as its specific Go type (e.g., `ConnectRequest`), or `nil` on error.
    *   `Probe`: The intermediate Probe struct containing the raw JSON fields (`Params`, `Result`, `Error` as `json.RawMessage`). This can be useful even if a decoding error occurred later.
    *   `error`: Any error encountered during decoding (including JSON syntax errors, validation errors, or unknown methods). Returns `io.EOF` if the input stream ends cleanly.

**Example:**

```go
import (
	"encoding/json"
	"net"
	"log"
	"github.com/WuKongIM/WuKongIM/pkg/jsonrpc"
	"io"
)

func handleConnection(conn net.Conn) {
	defer conn.Close()
	decoder := json.NewDecoder(conn)

	for {
		msg, probe, err := jsonrpc.Decode(decoder)
		if err != nil {
			if err == io.EOF {
				log.Println("Connection closed.")
				break
			}
			log.Printf("Decode error: %v. Probe: %+v\n", err, probe)
			// Handle error (e.g., send error response, close connection)
			break // Or continue depending on error handling strategy
		}

		// Handle the successfully decoded message based on its type
		switch m := msg.(type) {
		case jsonrpc.ConnectRequest:
			log.Printf("Received ConnectRequest: %+v\n", m)
			// Process connect request...
		case jsonrpc.SendRequest:
			log.Printf("Received SendRequest: %+v\n", m)
			// Process send request...
		case jsonrpc.GenericResponse:
             log.Printf("Received GenericResponse: %+v\n", m)
             // Process response... maybe use probe.ID to match with request
		case jsonrpc.RecvNotification:
			log.Printf("Received RecvNotification: %+v\n", m)
            // Process recv notification...
		default:
			log.Printf("Received unknown message type: %T. Probe: %+v\n", msg, probe)
		}
	}
}

```

## Message Types

The specific message structures (Requests, Responses, Notifications, Params, Results, etc.) are defined in `types.go`. Base types like `BaseRequest`, `BaseResponse`, and `BaseNotification` provide common fields.

## Current Limitations / Notes

*   **Header/Setting Handling in `Decode`:** The current `Decode` implementation primarily relies on `json.Unmarshal` populating fields based on struct tags. For message types like `SendRequest` that have top-level `Header` and `Setting` fields (outside of `Params`), `Decode` *does* attempt to populate these based on the initial probe. However, for types where `Header`/`Setting` might be expected *within* the `Params` structure (like `RecvNotification`), correct decoding depends on the `RecvNotificationParams` struct having corresponding fields with correct tags. Verify the struct definitions in `types.go` for accurate behavior.
*   **Unknown Methods:** `Decode` will return an error for requests or notifications with method names not explicitly handled in its internal `switch` statements. 