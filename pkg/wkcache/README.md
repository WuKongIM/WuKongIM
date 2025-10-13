# StreamCache - High-Performance Streaming Message Cache

StreamCache is a high-performance, thread-safe cache for streaming message chunks, designed for scenarios similar to OpenAI's streaming messages. It efficiently handles the caching of message chunks until a complete message is assembled, then flushes the complete message for database storage.

## Features

- **High Performance**: Optimized for concurrent access with minimal lock contention
- **Memory Management**: Built-in memory overflow protection and configurable limits
- **Thread Safety**: Full concurrent access support with proper synchronization
- **Automatic Cleanup**: Periodic cleanup of expired streams to prevent memory leaks
- **Flexible Configuration**: Customizable limits and timeouts
- **Error Handling**: Comprehensive error handling with specific error types
- **Monitoring**: Built-in statistics and monitoring capabilities

## Core Concepts

### MessageChunk
Represents a single chunk of a streaming message:
```go
type MessageChunk struct {
    MessageId int64  // Unique message identifier
    ChunkId   int64  // Chunk sequence number within the message
    Payload   []byte // Chunk data
}
```

### StreamMeta
Contains metadata for a streaming message:
```go
type StreamMeta struct {
    // Core fields
    MessageId int64     // Message identifier
    CreatedAt time.Time // Stream creation time
    UpdatedAt time.Time // Last update time
    Closed    bool      // Whether stream is closed

    // Common streaming message fields
    ChannelId   string    // Channel/conversation this stream belongs to
    ChannelType uint8     // Type of channel (person, group, etc.)
    FromUid     string    // Sender user ID
    ClientMsgNo string    // Client-side message number
    Timestamp   time.Time // Message timestamp

    // Custom metadata fields for extensibility
    CustomFields map[string]interface{} // Flexible storage for additional metadata
}
```

## Usage

### Basic Usage

```go
// Create cache with default settings
cache := NewStreamCache(nil)
defer cache.Close()

// Step 1: Open a stream before adding chunks (required)
messageId := int64(12345)
meta := NewStreamMeta(messageId)
err := cache.OpenStream(meta)
if err != nil {
    log.Fatalf("Failed to open stream: %v", err)
}

// Step 2: Append chunks as they arrive
chunk := &MessageChunk{
    MessageId: messageId,
    ChunkId:   0,
    Payload:   []byte("chunk data"),
}
err = cache.AppendChunk(chunk) // Will fail if stream doesn't exist

// Step 3: Manually end the stream when complete
err = cache.EndStream(messageId, EndReasonSuccess)
```

### Enhanced Metadata with Builder Pattern

```go
// Create comprehensive stream metadata using builder pattern
meta := NewStreamMetaBuilder(messageId).
    Channel("general", 1).
    From("user123").
    ClientMessage("msg-001").
    CustomField("priority", "high").
    CustomField("category", "notification").
    Build()

// Open stream with advanced metadata
err := cache.OpenStream(meta)
```

### Custom Configuration

```go
cache := NewStreamCache(&StreamCacheOptions{
    MaxMemorySize:          100 * 1024 * 1024, // 100MB limit
    MaxStreams:             10000,              // Max concurrent streams
    MaxChunksPerStream:     1000,               // Max chunks per stream
    StreamTimeout:          30 * time.Minute,   // Stream timeout
    ChunkInactivityTimeout: 30 * time.Second,   // Auto-complete after 30s of inactivity
    CleanupInterval:        5 * time.Minute,    // Cleanup every 5 minutes
    OnStreamComplete: func(messageId int64, chunks []*MessageChunk) error {
        // Handle completed stream (e.g., save to database)
        return database.SaveMessage(messageId, chunks)
    },
})
```

### Working with Custom Fields

```go
// Set custom fields on metadata
meta := NewStreamMeta(messageId)
meta.SetCustomField("priority", "high")
meta.SetCustomField("retryCount", 3)
meta.SetCustomField("encrypted", true)

// Get custom fields with type safety
if priority, exists := meta.GetCustomFieldString("priority"); exists {
    fmt.Printf("Priority: %s\n", priority)
}

if retryCount, exists := meta.GetCustomFieldInt("retryCount"); exists {
    fmt.Printf("Retry count: %d\n", retryCount)
}

if encrypted, exists := meta.GetCustomFieldBool("encrypted"); exists {
    fmt.Printf("Encrypted: %v\n", encrypted)
}

// Remove custom fields
meta.RemoveCustomField("retryCount")
```

### Manual Stream Completion

```go
// For streams where total chunk count is unknown
cache.AppendChunk(chunk1)
cache.AppendChunk(chunk2)
// ... more chunks

// Manually end the stream
err := cache.EndStream(messageId, EndReasonSuccess)
```

### Querying Streams

```go
// Get streams by channel
channelStreams := cache.GetStreamsByChannel("general", 1)

// Get streams by user
userStreams := cache.GetStreamsByUser("user123")

// Get streams by custom field
highPriorityStreams := cache.GetStreamsByCustomField("priority", "high")

// Update stream metadata
err := cache.UpdateStreamMeta(messageId, func(meta *StreamMeta) {
    meta.ChannelId = "updated-channel"
    meta.SetCustomField("updated", true)
})

// High-performance channel stream detection
hasActiveStreams := cache.HasStreamInChannel("general", 1)
```

### Automatic Inactivity Completion

Streams automatically complete after a period of inactivity (no new chunks received):

```go
cache := NewStreamCache(&StreamCacheOptions{
    ChunkInactivityTimeout: 30 * time.Second, // Auto-complete after 30s
    OnStreamComplete: func(messageId int64, chunks []*MessageChunk) error {
        log.Printf("Stream %d auto-completed due to inactivity", messageId)
        return database.SaveMessage(messageId, chunks)
    },
})

// Add chunks to stream
cache.AppendChunk(chunk1)
cache.AppendChunk(chunk2)

// Stream will auto-complete after 30 seconds of no new chunks
// No need to call EndStream() manually
```

**Inactivity Behavior:**
- **Activity Tracking**: Each `AppendChunk()` call resets the inactivity timer
- **Background Monitoring**: Cleanup process checks for inactive streams periodically
- **Automatic Completion**: Inactive streams trigger the normal completion flow
- **Thread Safety**: Inactivity checking doesn't interfere with manual `EndStream()` calls

### High-Performance Channel Detection

The `HasStreamInChannel` method provides optimized detection of active streams in a specific channel:

```go
// Check if there are any active streams in a channel
hasStreams := cache.HasStreamInChannel("channel-id", 1)

// Performance optimizations:
// - Uses read locks for better concurrency
// - Early return on first match
// - Minimal memory allocation
// - Only checks active (non-closed) streams
```

**Performance Characteristics:**
- **Concurrent Access**: Uses RLock for optimal read performance
- **Early Return**: Returns immediately on first match
- **Memory Efficient**: No intermediate allocations
- **Active Stream Filter**: Only considers non-closed streams

### Stream Completion Performance

The `EndStream` method is optimized for minimal lock contention:

```go
// EndStream releases locks before executing callbacks
err := cache.EndStream(messageId, EndReasonSuccess)
```

**Lock Optimization:**
- **Minimal Critical Section**: Write lock held only for cache state changes
- **Lock-Free Callbacks**: `OnStreamComplete` executes outside critical sections
- **Concurrent Operations**: Other cache operations proceed while callbacks run
- **Thread Safety**: Stream removal and counter updates are atomic

## Stream Workflow

**Required Workflow:**

1. **Open Stream**: Call `OpenStream(meta)` to create a stream before adding chunks
2. **Append Chunks**: Call `AppendChunk(chunk)` to add data (requires existing stream)
3. **Complete Stream**: Either call `EndStream(messageId, reason)` manually or let inactivity timeout handle it

**Important Changes:**
- **Explicit Stream Creation**: Streams must be opened with `OpenStream()` before chunks can be appended
- **No Auto-Creation**: `AppendChunk()` will return `ErrStreamNotFound` for non-existent streams
- **Backward Compatibility**: The old `SetStreamMeta()` method has been removed

## Stream Completion Behavior

**Stream completion occurs in two ways:**

1. **Manual Completion**: Call `EndStream(messageId, reason)` to explicitly complete a stream
2. **Automatic Inactivity Completion**: Streams automatically complete after a configurable period of inactivity (no new chunks received)

### End Reason Tracking

The `EndReason` field in `StreamMeta` tracks why each stream was completed:

```go
// EndReason constants
const (
    EndReasonSuccess   = 0  // Stream completed successfully (default)
    EndReasonTimeout   = 1  // Stream ended due to inactivity timeout
    EndReasonError     = 2  // Stream ended due to an error
    EndReasonCancelled = 3  // Stream was manually cancelled
    EndReasonForce     = 4  // Stream was forcefully ended (e.g., channel closure)
)

// Manual completion with specific reason
err := cache.EndStream(messageId, EndReasonSuccess)

// Cancellation
err := cache.EndStream(messageId, EndReasonCancelled)

// Automatic timeout completion (handled internally)
// Sets EndReasonTimeout automatically
```

**EndReason Benefits:**
- **Debugging**: Track why streams were completed
- **Analytics**: Monitor completion patterns
- **Error Handling**: Distinguish between success and failure cases
- **Automatic Tracking**: Inactivity timeouts automatically set `EndReasonTimeout`

### Channel-Wide Stream Management

End all active streams within a specific channel:

```go
// End all streams in channel "general" of type 1 with force reason
err := cache.EndStreamInChannel("general", 1, EndReasonForce)
if err != nil {
    log.Printf("Failed to end streams in channel: %v", err)
}

// End all streams in a private channel due to error
err := cache.EndStreamInChannel("private-123", 2, EndReasonError)
```

**Channel Management Features:**
- **Bulk Operations**: End multiple streams in a single call
- **Channel Filtering**: Target specific channel ID and type combinations
- **Concurrent Safe**: Thread-safe operation with proper locking
- **Error Aggregation**: Collects and reports individual stream errors
- **Selective Targeting**: Only affects streams matching exact channel criteria

## Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `MaxMemorySize` | `int64` | 100MB | Maximum memory usage in bytes |
| `MaxStreams` | `int` | 10000 | Maximum concurrent streams |
| `MaxChunksPerStream` | `int` | 1000 | Maximum chunks per stream |
| `StreamTimeout` | `time.Duration` | 30min | Timeout for inactive streams (cleanup) |
| `ChunkInactivityTimeout` | `time.Duration` | 30sec | Timeout for auto-completing inactive streams |
| `CleanupInterval` | `time.Duration` | 5min | Interval for cleanup operations |
| `OnStreamComplete` | `func` | nil | Callback when stream completes |

## Error Handling

The cache provides specific error types for different failure scenarios:

- `ErrStreamNotFound`: Stream doesn't exist
- `ErrStreamClosed`: Attempting to modify a closed stream
- `ErrMemoryLimitReached`: Memory usage would exceed limit
- `ErrTooManyStreams`: Maximum concurrent streams exceeded
- `ErrTooManyChunks`: Maximum chunks per stream exceeded
- `ErrInvalidMessageId`: Invalid message ID (≤ 0)
- `ErrInvalidChunkId`: Invalid chunk ID (< 0)
- `ErrDuplicateChunk`: Chunk with same ID already exists

## Monitoring and Statistics

```go
// Get cache statistics
stats := cache.GetStats()
fmt.Printf("Memory usage: %d bytes\n", stats["memory_usage"])
fmt.Printf("Active streams: %d\n", stats["stream_count"])

// Check memory usage
memoryUsage := cache.GetMemoryUsage()
streamCount := cache.GetStreamCount()

// Check if approaching memory limit
if cache.IsMemoryLimitReached(0.9) { // 90% threshold
    // Take action to reduce memory usage
}
```

## Performance Characteristics

- **Memory Efficiency**: Minimal overhead per chunk and stream
- **Concurrent Access**: Optimized for high-concurrency scenarios
- **Lock-Free Callbacks**: Stream completion callbacks execute outside critical sections
- **Minimal Lock Contention**: Write locks held only for cache state changes
- **Automatic Cleanup**: Background cleanup prevents memory leaks
- **Lock Optimization**: Read-write locks minimize contention
- **Atomic Operations**: Lock-free operations where possible

## Thread Safety

All public methods are thread-safe and can be called concurrently from multiple goroutines. The implementation uses:

- Read-write mutexes for optimal read performance
- Atomic operations for counters and statistics
- Proper synchronization for all shared state

## Best Practices

1. **Set Stream Metadata**: Always call `SetStreamMeta()` when the total chunk count is known
2. **Handle Completion**: Provide an `OnStreamComplete` callback for proper message handling
3. **Monitor Memory**: Regularly check memory usage in high-throughput scenarios
4. **Configure Limits**: Set appropriate limits based on your use case
5. **Error Handling**: Always check and handle returned errors
6. **Graceful Shutdown**: Call `Close()` to properly cleanup resources

## Integration Example

```go
// Example integration with a message handler
type MessageHandler struct {
    cache *StreamCache
    db    Database
}

func NewMessageHandler(db Database) *MessageHandler {
    return &MessageHandler{
        db: db,
        cache: NewStreamCache(&StreamCacheOptions{
            OnStreamComplete: func(messageId int64, chunks []*MessageChunk) error {
                return db.SaveCompleteMessage(messageId, chunks)
            },
        }),
    }
}

func (h *MessageHandler) HandleChunk(chunk *MessageChunk) error {
    return h.cache.AppendChunk(chunk)
}

func (h *MessageHandler) EndStream(messageId int64, reason int) error {
    return h.cache.EndStream(messageId, reason)
}

func (h *MessageHandler) EndStreamInChannel(channelId string, channelType uint8, reason int) error {
    return h.cache.EndStreamInChannel(channelId, channelType, reason)
}

func (h *MessageHandler) HasActiveStreamsInChannel(channelId string, channelType uint8) bool {
    return h.cache.HasStreamInChannel(channelId, channelType)
}

func (h *MessageHandler) Close() error {
    return h.cache.Close()
}
```
