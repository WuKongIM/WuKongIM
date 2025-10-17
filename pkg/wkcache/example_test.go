package wkcache

import (
	"fmt"
	"log"
	"time"
)

// Example demonstrates basic usage of StreamCache
func ExampleStreamCache() {
	// Create a new stream cache with custom options
	cache := NewStreamCache(&StreamCacheOptions{
		MaxMemorySize:      10 * 1024 * 1024, // 10MB
		MaxStreams:         1000,             // Max 1000 concurrent streams
		MaxChunksPerStream: 500,              // Max 500 chunks per stream
		StreamTimeout:      5 * time.Minute,  // 5 minute timeout
		OnStreamComplete: func(meta *StreamMeta, chunks []*MessageChunk) error {
			// This callback is called when a stream is complete
			fmt.Printf("Stream %s completed with %d chunks\n", meta.ClientMsgNo, len(chunks))

			// Here you would typically save the complete message to database
			// For example:
			// return database.SaveMessage(clientMsgNo, chunks)

			return nil
		},
	})
	defer cache.Close()

	clientMsgNo := "msg_12345"

	// Step 1: Open stream with metadata (required)
	meta := NewStreamMeta(clientMsgNo)
	err := cache.OpenStream(meta)
	if err != nil {
		log.Fatalf("Failed to open stream: %v", err)
	}

	// Step 2: Append chunks as they arrive
	chunks := []*MessageChunk{
		{ClientMsgNo: clientMsgNo, ChunkId: 0, Payload: []byte("Hello ")},
		{ClientMsgNo: clientMsgNo, ChunkId: 1, Payload: []byte("streaming ")},
		{ClientMsgNo: clientMsgNo, ChunkId: 2, Payload: []byte("world!")},
	}

	for _, chunk := range chunks {
		err := cache.AppendChunk(chunk)
		if err != nil {
			log.Fatalf("Failed to append chunk: %v", err)
		}
	}

	// Manually end the stream to trigger completion
	err = cache.EndStream(clientMsgNo, EndReasonSuccess)
	if err != nil {
		log.Fatalf("Failed to end stream: %v", err)
	}

	// The OnStreamComplete callback will be called

	// Output: Stream msg_12345 completed with 3 chunks
}

// Example demonstrates manual stream completion
func ExampleStreamCache_EndStream() {
	cache := NewStreamCache(&StreamCacheOptions{
		OnStreamComplete: func(meta *StreamMeta, chunks []*MessageChunk) error {
			fmt.Printf("Manually completed stream %s with %d chunks\n", meta.ClientMsgNo, len(chunks))
			return nil
		},
	})
	defer cache.Close()

	clientMsgNo := "msg_67890"

	// Open stream first
	meta := NewStreamMeta(clientMsgNo)
	cache.OpenStream(meta)

	// Add some chunks
	cache.AppendChunk(&MessageChunk{
		ClientMsgNo: clientMsgNo,
		ChunkId:     0,
		Payload:     []byte("Chunk 1"),
	})

	cache.AppendChunk(&MessageChunk{
		ClientMsgNo: clientMsgNo,
		ChunkId:     1,
		Payload:     []byte("Chunk 2"),
	})

	// Manually end the stream when no more chunks are expected
	err := cache.EndStream(clientMsgNo, EndReasonSuccess)
	if err != nil {
		log.Fatalf("Failed to end stream: %v", err)
	}

	// Output: Manually completed stream msg_67890 with 2 chunks
}

// Example demonstrates error handling and monitoring
func ExampleStreamCache_monitoring() {
	cache := NewStreamCache(&StreamCacheOptions{
		MaxMemorySize: 1024, // Very small limit for demonstration
		MaxStreams:    2,    // Only 2 concurrent streams
	})
	defer cache.Close()

	// Monitor cache statistics
	stats := cache.GetStats()
	fmt.Printf("Initial stats: %+v\n", stats)

	// Open stream first
	meta := NewStreamMeta("msg_1")
	cache.OpenStream(meta)

	// Try to add a large chunk that exceeds memory limit
	largeChunk := &MessageChunk{
		ClientMsgNo: "msg_1",
		ChunkId:     0,
		Payload:     make([]byte, 2000), // Larger than memory limit
	}

	err := cache.AppendChunk(largeChunk)
	if err == ErrMemoryLimitReached {
		fmt.Println("Memory limit reached - chunk rejected")
	}

	// Add a normal chunk
	normalChunk := &MessageChunk{
		ClientMsgNo: "msg_1",
		ChunkId:     0,
		Payload:     []byte("Normal chunk"),
	}

	err = cache.AppendChunk(normalChunk)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("Memory usage: %d bytes\n", cache.GetMemoryUsage())
		fmt.Printf("Stream count: %d\n", cache.GetStreamCount())
	}

	// Check if memory limit is approaching
	if cache.IsMemoryLimitReached(0.8) { // 80% threshold
		fmt.Println("Memory usage is approaching limit")
	}

	// Output:
	// Initial stats: map[max_memory:1024 max_streams:2 memory_percent:0 memory_usage:0 stream_count:0]
	// Memory limit reached - chunk rejected
	// Memory usage: 28 bytes
	// Stream count: 1
}
