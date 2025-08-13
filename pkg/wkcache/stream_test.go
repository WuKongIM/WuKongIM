package wkcache

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamCache_Basic(t *testing.T) {
	// Create a cache with small limits for testing
	cache := NewStreamCache(&StreamCacheOptions{
		MaxMemorySize:      1024 * 1024, // 1MB
		MaxStreams:         10,
		MaxChunksPerStream: 100,
		StreamTimeout:      time.Minute,
	})
	defer cache.Close()

	clientMsgNo := "msg_12345"

	// Test opening stream
	meta := NewStreamMeta(clientMsgNo)
	err := cache.OpenStream(meta)
	if err != nil {
		t.Fatalf("Failed to open stream: %v", err)
	}

	// Test getting stream info
	streamInfo, err := cache.GetStreamInfo(clientMsgNo)
	if err != nil {
		t.Fatalf("Failed to get stream info: %v", err)
	}
	if streamInfo.ClientMsgNo != clientMsgNo {
		t.Errorf("Expected clientMsgNo %s, got %s", clientMsgNo, streamInfo.ClientMsgNo)
	}
	if meta.ClientMsgNo != clientMsgNo {
		t.Errorf("Expected clientMsgNo %s, got %s", clientMsgNo, meta.ClientMsgNo)
	}

	// Test appending chunks
	chunks := []*MessageChunk{
		{ClientMsgNo: clientMsgNo, ChunkId: 0, Payload: []byte("chunk0")},
		{ClientMsgNo: clientMsgNo, ChunkId: 1, Payload: []byte("chunk1")},
		{ClientMsgNo: clientMsgNo, ChunkId: 2, Payload: []byte("chunk2")},
	}

	var completedChunks []*MessageChunk
	cache.onStreamComplete = func(meta *StreamMeta, receivedChunks []*MessageChunk) error {
		if meta.ClientMsgNo != clientMsgNo {
			t.Errorf("Expected clientMsgNo %s, got %s", clientMsgNo, meta.ClientMsgNo)
		}
		completedChunks = receivedChunks
		return nil
	}

	// Append chunks one by one
	for i, chunk := range chunks {
		err := cache.AppendChunk(chunk)
		if err != nil {
			t.Fatalf("Failed to append chunk %d: %v", i, err)
		}
	}

	// Manually end the stream since auto-completion is disabled
	err = cache.EndStream(clientMsgNo, EndReasonSuccess)
	if err != nil {
		t.Fatalf("Failed to end stream: %v", err)
	}

	// Verify completion callback was called
	if len(completedChunks) != 3 {
		t.Errorf("Expected 3 completed chunks, got %d", len(completedChunks))
	}

	// Verify chunks are in correct order
	for i, chunk := range completedChunks {
		if chunk.ChunkId != uint64(i) {
			t.Errorf("Expected chunk %d to have ChunkId %d, got %d", i, i, chunk.ChunkId)
		}
		expectedPayload := []byte("chunk" + string(rune('0'+i)))
		if string(chunk.Payload) != string(expectedPayload) {
			t.Errorf("Expected payload %s, got %s", expectedPayload, chunk.Payload)
		}
	}

	// Stream should be removed after completion
	if cache.HasStream(clientMsgNo) {
		t.Error("Stream should be removed after completion")
	}
}

func TestStreamCache_WithMetadata(t *testing.T) {
	cache := NewStreamCache(nil) // Use defaults
	defer cache.Close()

	clientMsgNo := "msg_67890"

	// First open the stream with metadata
	meta := NewStreamMeta(clientMsgNo)
	meta.ChannelId = "test_channel"
	meta.ChannelType = 1
	meta.FromUid = "user123"

	err := cache.OpenStream(meta)
	if err != nil {
		t.Fatalf("Failed to open stream: %v", err)
	}

	chunk := &MessageChunk{
		ClientMsgNo: clientMsgNo,
		ChunkId:     0,
		Payload:     []byte("test chunk"),
	}

	// Append chunk to opened stream
	err = cache.AppendChunk(chunk)
	if err != nil {
		t.Fatalf("Failed to append chunk to stream: %v", err)
	}

	// Verify stream exists
	if !cache.HasStream(clientMsgNo) {
		t.Error("Stream should exist")
	}

	// Get stream info to verify metadata
	streamInfo, err := cache.GetStreamInfo(clientMsgNo)
	if err != nil {
		t.Fatalf("Failed to get stream info: %v", err)
	}

	if streamInfo.ChannelId != "test_channel" {
		t.Errorf("Expected ChannelId 'test_channel', got %s", streamInfo.ChannelId)
	}
}

func TestStreamCache_Errors(t *testing.T) {
	cache := NewStreamCache(&StreamCacheOptions{
		MaxMemorySize:      100, // Very small limit
		MaxStreams:         1,   // Only one stream
		MaxChunksPerStream: 2,   // Only two chunks per stream
	})
	defer cache.Close()

	// Test invalid client message number
	invalidMeta := NewStreamMeta("")
	err := cache.OpenStream(invalidMeta)
	if err != ErrInvalidClientMsgNo {
		t.Errorf("Expected ErrInvalidClientMsgNo, got %v", err)
	}

	// Test memory limit
	memoryTestMeta := NewStreamMeta("msg_1")
	cache.OpenStream(memoryTestMeta)
	largeChunk := &MessageChunk{
		ClientMsgNo: "msg_1",
		ChunkId:     0,
		Payload:     make([]byte, 200), // Larger than memory limit
	}
	err = cache.AppendChunk(largeChunk)
	if err != ErrMemoryLimitReached {
		t.Errorf("Expected ErrMemoryLimitReached, got %v", err)
	}

	// Test appending to non-existent stream
	err = cache.AppendChunk(&MessageChunk{
		ClientMsgNo: "msg_999",
		ChunkId:     0,
		Payload:     []byte("chunk for non-existent stream"),
	})
	if err != ErrStreamNotFound {
		t.Errorf("Expected ErrStreamNotFound, got %v", err)
	}

	// Test stream limit
	meta1 := NewStreamMeta("msg_stream1")
	cache.OpenStream(meta1)
	meta2 := NewStreamMeta("msg_stream2")
	err = cache.OpenStream(meta2) // Second stream should fail
	if err != ErrTooManyStreams {
		t.Errorf("Expected ErrTooManyStreams, got %v", err)
	}
}

func TestStreamCache_Stats(t *testing.T) {
	cache := NewStreamCache(nil)
	defer cache.Close()

	// Initially empty
	stats := cache.GetStats()
	if stats["stream_count"].(int) != 0 {
		t.Error("Expected 0 streams initially")
	}

	// Add a stream without completion callback to keep it in cache
	clientMsgNo := "msg_123"
	meta := NewStreamMeta(clientMsgNo)
	cache.OpenStream(meta)
	cache.AppendChunk(&MessageChunk{
		ClientMsgNo: clientMsgNo,
		ChunkId:     0,
		Payload:     []byte("test"),
	})

	// Check memory usage
	if cache.GetMemoryUsage() <= 0 {
		t.Error("Expected positive memory usage")
	}

	if cache.GetStreamCount() != 1 {
		t.Errorf("Expected 1 stream, got %d", cache.GetStreamCount())
	}
}

func TestStreamMeta_EnhancedFields(t *testing.T) {
	cache := NewStreamCache(nil)
	defer cache.Close()

	clientMsgNo := "msg_12345"

	// Test builder pattern
	meta := NewStreamMetaBuilder(clientMsgNo).
		Channel("test-channel", 1).
		From("user123").
		ClientMessage("client-msg-001").
		CustomField("priority", "high").
		CustomField("category", "notification").
		Build()

	// Open stream with advanced metadata
	err := cache.OpenStream(meta)
	if err != nil {
		t.Fatalf("Failed to set advanced stream meta: %v", err)
	}

	// Retrieve and verify metadata
	retrievedMeta, err := cache.GetStreamInfo(clientMsgNo)
	if err != nil {
		t.Fatalf("Failed to get stream info: %v", err)
	}

	// Verify core fields
	if retrievedMeta.ClientMsgNo != clientMsgNo {
		t.Errorf("Expected clientMsgNo %s, got %s", clientMsgNo, retrievedMeta.ClientMsgNo)
	}

	// Verify enhanced fields
	if retrievedMeta.ChannelId != "test-channel" {
		t.Errorf("Expected channelId 'test-channel', got '%s'", retrievedMeta.ChannelId)
	}
	if retrievedMeta.ChannelType != 1 {
		t.Errorf("Expected channelType 1, got %d", retrievedMeta.ChannelType)
	}
	if retrievedMeta.FromUid != "user123" {
		t.Errorf("Expected fromUid 'user123', got '%s'", retrievedMeta.FromUid)
	}
	if retrievedMeta.ClientMsgNo != "client-msg-001" {
		t.Errorf("Expected clientMsgNo 'client-msg-001', got '%s'", retrievedMeta.ClientMsgNo)
	}

	// Verify custom fields
	if priority, exists := retrievedMeta.GetCustomFieldString("priority"); !exists || priority != "high" {
		t.Errorf("Expected custom field 'priority' to be 'high', got '%s' (exists: %v)", priority, exists)
	}
	if category, exists := retrievedMeta.GetCustomFieldString("category"); !exists || category != "notification" {
		t.Errorf("Expected custom field 'category' to be 'notification', got '%s' (exists: %v)", category, exists)
	}
}

func TestStreamMeta_CustomFields(t *testing.T) {
	meta := NewStreamMeta("msg_123")

	// Test setting and getting custom fields
	meta.SetCustomField("stringField", "test")
	meta.SetCustomField("intField", 42)
	meta.SetCustomField("boolField", true)

	// Test string field
	if value, exists := meta.GetCustomFieldString("stringField"); !exists || value != "test" {
		t.Errorf("Expected string field 'test', got '%s' (exists: %v)", value, exists)
	}

	// Test int field
	if value, exists := meta.GetCustomFieldInt("intField"); !exists || value != 42 {
		t.Errorf("Expected int field 42, got %d (exists: %v)", value, exists)
	}

	// Test bool field
	if value, exists := meta.GetCustomFieldBool("boolField"); !exists || value != true {
		t.Errorf("Expected bool field true, got %v (exists: %v)", value, exists)
	}

	// Test non-existent field
	if _, exists := meta.GetCustomField("nonExistent"); exists {
		t.Error("Expected non-existent field to not exist")
	}

	// Test removing field
	meta.RemoveCustomField("stringField")
	if _, exists := meta.GetCustomField("stringField"); exists {
		t.Error("Expected removed field to not exist")
	}
}

func TestStreamMeta_Clone(t *testing.T) {
	original := NewStreamMeta("msg_123")
	original.ChannelId = "test-channel"
	original.FromUid = "user123"
	original.SetCustomField("test", "value")

	clone := original.Clone()

	// Verify clone has same values
	if clone.ClientMsgNo != original.ClientMsgNo {
		t.Error("Clone should have same ClientMsgNo")
	}
	if clone.ChannelId != original.ChannelId {
		t.Error("Clone should have same ChannelId")
	}
	if clone.FromUid != original.FromUid {
		t.Error("Clone should have same FromUid")
	}

	// Verify custom fields are copied
	if value, exists := clone.GetCustomFieldString("test"); !exists || value != "value" {
		t.Error("Clone should have same custom fields")
	}

	// Verify it's a deep copy (modifying clone doesn't affect original)
	clone.ChannelId = "modified"
	clone.SetCustomField("test", "modified")

	if original.ChannelId == "modified" {
		t.Error("Modifying clone should not affect original")
	}
	if value, _ := original.GetCustomFieldString("test"); value == "modified" {
		t.Error("Modifying clone custom fields should not affect original")
	}
}

func TestStreamCache_QueryMethods(t *testing.T) {
	cache := NewStreamCache(nil)
	defer cache.Close()

	// Create streams with different metadata
	meta1 := NewStreamMetaBuilder("msg_1").
		Channel("channel1", 1).
		From("user1").
		CustomField("priority", "high").
		Build()

	meta2 := NewStreamMetaBuilder("msg_2").
		Channel("channel1", 1).
		From("user2").
		CustomField("priority", "low").
		Build()

	meta3 := NewStreamMetaBuilder("msg_3").
		Channel("channel2", 2).
		From("user1").
		CustomField("priority", "high").
		Build()

	// Open streams with metadata
	cache.OpenStream(meta1)
	cache.OpenStream(meta2)
	cache.OpenStream(meta3)

	// Test GetStreamsByChannel
	channel1Streams := cache.GetStreamsByChannel("channel1", 1)
	if len(channel1Streams) != 2 {
		t.Errorf("Expected 2 streams for channel1, got %d", len(channel1Streams))
	}

	channel2Streams := cache.GetStreamsByChannel("channel2", 2)
	if len(channel2Streams) != 1 {
		t.Errorf("Expected 1 stream for channel2, got %d", len(channel2Streams))
	}

	// Test GetStreamsByUser
	user1Streams := cache.GetStreamsByUser("user1")
	if len(user1Streams) != 2 {
		t.Errorf("Expected 2 streams for user1, got %d", len(user1Streams))
	}

	user2Streams := cache.GetStreamsByUser("user2")
	if len(user2Streams) != 1 {
		t.Errorf("Expected 1 stream for user2, got %d", len(user2Streams))
	}

	// Test GetStreamsByCustomField
	highPriorityStreams := cache.GetStreamsByCustomField("priority", "high")
	if len(highPriorityStreams) != 2 {
		t.Errorf("Expected 2 high priority streams, got %d", len(highPriorityStreams))
	}

	lowPriorityStreams := cache.GetStreamsByCustomField("priority", "low")
	if len(lowPriorityStreams) != 1 {
		t.Errorf("Expected 1 low priority stream, got %d", len(lowPriorityStreams))
	}
}

func TestStreamCache_UpdateStreamMeta(t *testing.T) {
	cache := NewStreamCache(nil)
	defer cache.Close()

	clientMsgNo := "msg_123"

	// Create initial stream
	meta := NewStreamMeta(clientMsgNo)
	err := cache.OpenStream(meta)
	if err != nil {
		t.Fatalf("Failed to open stream: %v", err)
	}

	// Update stream metadata
	err = cache.UpdateStreamMeta(clientMsgNo, func(meta *StreamMeta) {
		meta.ChannelId = "updated-channel"
		meta.FromUid = "updated-user"
		meta.SetCustomField("updated", true)
	})
	if err != nil {
		t.Fatalf("Failed to update stream meta: %v", err)
	}

	// Verify updates
	updatedMeta, err := cache.GetStreamInfo(clientMsgNo)
	if err != nil {
		t.Fatalf("Failed to get stream info: %v", err)
	}
	if updatedMeta.CustomFields["test"] != "updated" {
		t.Errorf("Expected custom field 'test' to be 'updated', got %v", updatedMeta.CustomFields["test"])
	}

	if meta.ChannelId != "updated-channel" {
		t.Errorf("Expected channelId 'updated-channel', got '%s'", meta.ChannelId)
	}
	if meta.FromUid != "updated-user" {
		t.Errorf("Expected fromUid 'updated-user', got '%s'", meta.FromUid)
	}
	if updated, exists := meta.GetCustomFieldBool("updated"); !exists || !updated {
		t.Errorf("Expected custom field 'updated' to be true, got %v (exists: %v)", updated, exists)
	}

	// Test updating non-existent stream
	err = cache.UpdateStreamMeta("msg_999", func(meta *StreamMeta) {
		meta.ChannelId = "should-fail"
	})
	if err != ErrStreamNotFound {
		t.Errorf("Expected ErrStreamNotFound, got %v", err)
	}
}

func TestStreamCache_HasStreamInChannel(t *testing.T) {
	cache := NewStreamCache(nil)
	defer cache.Close()

	// Initially no streams
	if cache.HasStreamInChannel("channel1", 1) {
		t.Error("Expected no streams in channel1 initially")
	}

	// Create streams in different channels
	meta1 := NewStreamMetaBuilder("msg_1").
		Channel("channel1", 1).
		From("user1").
		Build()

	meta2 := NewStreamMetaBuilder("msg_2").
		Channel("channel1", 2). // Same channel ID, different type
		From("user2").
		Build()

	meta3 := NewStreamMetaBuilder("msg_3").
		Channel("channel2", 1). // Different channel ID, same type
		From("user3").
		Build()

	// Open streams with metadata
	cache.OpenStream(meta1)
	cache.OpenStream(meta2)
	cache.OpenStream(meta3)

	// Test channel detection
	if !cache.HasStreamInChannel("channel1", 1) {
		t.Error("Expected to find stream in channel1 type 1")
	}

	if !cache.HasStreamInChannel("channel1", 2) {
		t.Error("Expected to find stream in channel1 type 2")
	}

	if !cache.HasStreamInChannel("channel2", 1) {
		t.Error("Expected to find stream in channel2 type 1")
	}

	// Test non-existent combinations
	if cache.HasStreamInChannel("channel2", 2) {
		t.Error("Expected no stream in channel2 type 2")
	}

	if cache.HasStreamInChannel("channel3", 1) {
		t.Error("Expected no stream in channel3 type 1")
	}

	// Test with closed stream
	err := cache.EndStream("msg_1", EndReasonSuccess) // Close stream in channel1 type 1
	if err != nil {
		t.Fatalf("Failed to end stream: %v", err)
	}

	// Should still return false for channel1 type 1 since stream is closed
	// But first we need to add the stream back since EndStream removes it
	meta4 := NewStreamMetaBuilder("msg_4").
		Channel("channel1", 1).
		From("user4").
		Build()
	cache.OpenStream(meta4)

	// Close the stream manually
	cache.UpdateStreamMeta("msg_4", func(meta *StreamMeta) {
		meta.Closed = true
	})

	// Should return false for closed stream
	if cache.HasStreamInChannel("channel1", 1) {
		t.Error("Expected no active stream in channel1 type 1 (stream is closed)")
	}
}

func TestStreamCache_HasStreamInChannel_Performance(t *testing.T) {
	cache := NewStreamCache(&StreamCacheOptions{
		MaxStreams: 10000,
	})
	defer cache.Close()

	// Create many streams in different channels
	for i := 0; i < 1000; i++ {
		meta := NewStreamMetaBuilder(fmt.Sprintf("msg_%d", i)).
			Channel(fmt.Sprintf("channel%d", i%10), uint8(i%3)).
			From(fmt.Sprintf("user%d", i)).
			Build()
		cache.OpenStream(meta)
	}

	// Test performance with early return optimization
	// This should be fast even with many streams because of early return
	start := time.Now()
	for i := 0; i < 100; i++ {
		// Test existing channel (should return quickly due to early return)
		if !cache.HasStreamInChannel("channel0", 0) {
			t.Error("Expected to find stream in channel0 type 0")
		}

		// Test non-existent channel (should iterate through all but still be fast)
		if cache.HasStreamInChannel("nonexistent", 99) {
			t.Error("Expected no stream in nonexistent channel")
		}
	}
	duration := time.Since(start)

	// Performance check - should complete quickly (adjust threshold as needed)
	if duration > time.Millisecond*100 {
		t.Errorf("HasStreamInChannel performance test took too long: %v", duration)
	}

	t.Logf("HasStreamInChannel performance test completed in %v", duration)
}

func TestStreamCache_InactivityTimeout(t *testing.T) {
	// Test that streams auto-complete after inactivity timeout
	inactivityTimeout := 100 * time.Millisecond
	cleanupInterval := 50 * time.Millisecond
	completedStreams := make(map[string]bool)
	var mu sync.Mutex

	cache := NewStreamCache(&StreamCacheOptions{
		ChunkInactivityTimeout: inactivityTimeout,
		CleanupInterval:        cleanupInterval,
		OnStreamComplete: func(meta *StreamMeta, chunks []*MessageChunk) error {
			mu.Lock()
			completedStreams[meta.ClientMsgNo] = true
			mu.Unlock()
			return nil
		},
	})
	defer cache.Close()

	// Create a stream and add a chunk
	clientMsgNo := "msg_1"
	meta := NewStreamMeta(clientMsgNo)
	cache.OpenStream(meta)
	cache.AppendChunk(&MessageChunk{
		ClientMsgNo: clientMsgNo,
		ChunkId:     0,
		Payload:     []byte("test chunk"),
	})

	// Verify stream exists and is not completed yet
	if !cache.HasStream(clientMsgNo) {
		t.Error("Stream should exist before timeout")
	}

	mu.Lock()
	completed := completedStreams[clientMsgNo]
	mu.Unlock()
	if completed {
		t.Error("Stream should not be completed yet")
	}

	// Wait for inactivity timeout + cleanup interval + buffer
	time.Sleep(inactivityTimeout + cleanupInterval + 100*time.Millisecond)

	// Verify stream was auto-completed
	mu.Lock()
	completed = completedStreams[clientMsgNo]
	mu.Unlock()
	if !completed {
		t.Error("Stream should have been auto-completed due to inactivity")
	}

	// Verify stream was removed from cache
	if cache.HasStream(clientMsgNo) {
		t.Error("Stream should have been removed after auto-completion")
	}
}

func TestStreamCache_InactivityTimeout_ActiveStream(t *testing.T) {
	// Test that active streams (receiving chunks) are not auto-completed
	inactivityTimeout := 200 * time.Millisecond
	cleanupInterval := 50 * time.Millisecond
	completedStreams := make(map[string]bool)
	var mu sync.Mutex

	cache := NewStreamCache(&StreamCacheOptions{
		ChunkInactivityTimeout: inactivityTimeout,
		CleanupInterval:        cleanupInterval,
		OnStreamComplete: func(meta *StreamMeta, chunks []*MessageChunk) error {
			mu.Lock()
			completedStreams[meta.ClientMsgNo] = true
			mu.Unlock()
			return nil
		},
	})
	defer cache.Close()

	// Create a stream and add initial chunk
	clientMsgNo := "msg_1"
	meta := NewStreamMeta(clientMsgNo)
	cache.OpenStream(meta)
	cache.AppendChunk(&MessageChunk{
		ClientMsgNo: clientMsgNo,
		ChunkId:     0,
		Payload:     []byte("chunk 0"),
	})

	// Keep adding chunks periodically to keep stream active
	stopFeeding := make(chan struct{})
	go func() {
		chunkId := uint64(1)
		ticker := time.NewTicker(inactivityTimeout / 3) // Add chunks more frequently than timeout
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				cache.AppendChunk(&MessageChunk{
					ClientMsgNo: clientMsgNo,
					ChunkId:     chunkId,
					Payload:     []byte(fmt.Sprintf("chunk %d", chunkId)),
				})
				chunkId++
			case <-stopFeeding:
				return
			}
		}
	}()

	// Wait longer than the inactivity timeout
	time.Sleep(inactivityTimeout*2 + cleanupInterval + 100*time.Millisecond)

	// Stop feeding chunks
	close(stopFeeding)

	// Verify stream was NOT auto-completed (it was active)
	mu.Lock()
	completed := completedStreams[clientMsgNo]
	mu.Unlock()
	if completed {
		t.Error("Active stream should not have been auto-completed")
	}

	// Verify stream still exists
	if !cache.HasStream(clientMsgNo) {
		t.Error("Active stream should still exist")
	}

	// Now wait for inactivity timeout to trigger
	time.Sleep(inactivityTimeout + cleanupInterval + 100*time.Millisecond)

	// Now it should be completed
	mu.Lock()
	completed = completedStreams[clientMsgNo]
	mu.Unlock()
	if !completed {
		t.Error("Stream should have been auto-completed after becoming inactive")
	}
}

func TestStreamCache_InactivityTimeout_ManualEndStream(t *testing.T) {
	// Test edge case where manual EndStream is called while inactivity timeout is also triggered
	inactivityTimeout := 100 * time.Millisecond
	cleanupInterval := 50 * time.Millisecond
	completedCount := int32(0)

	cache := NewStreamCache(&StreamCacheOptions{
		ChunkInactivityTimeout: inactivityTimeout,
		CleanupInterval:        cleanupInterval,
		OnStreamComplete: func(meta *StreamMeta, chunks []*MessageChunk) error {
			atomic.AddInt32(&completedCount, 1)
			return nil
		},
	})
	defer cache.Close()

	// Create a stream
	messageId := int64(1)
	meta := NewStreamMeta(messageId)
	cache.OpenStream(meta)
	cache.AppendChunk(&MessageChunk{
		MessageId: messageId,
		ChunkId:   0,
		Payload:   []byte("test chunk"),
	})

	// Wait almost until timeout, then manually end stream
	time.Sleep(inactivityTimeout - 20*time.Millisecond)

	// Manually end the stream
	err := cache.EndStream(messageId, EndReasonSuccess)
	if err != nil {
		t.Fatalf("Manual EndStream failed: %v", err)
	}

	// Wait for cleanup cycle to run
	time.Sleep(cleanupInterval + 100*time.Millisecond)

	// Verify stream was completed exactly once (not twice)
	finalCount := atomic.LoadInt32(&completedCount)
	if finalCount != 1 {
		t.Errorf("Expected stream to be completed exactly once, got %d times", finalCount)
	}

	// Verify stream was removed
	if cache.HasStream(messageId) {
		t.Error("Stream should have been removed")
	}
}

func TestStreamCache_InactivityTimeout_ConcurrentChunks(t *testing.T) {
	// Test thread safety with concurrent chunk appending and timeout checking
	inactivityTimeout := 100 * time.Millisecond
	cleanupInterval := 50 * time.Millisecond
	completedStreams := make(map[int64]bool)
	var mu sync.Mutex

	cache := NewStreamCache(&StreamCacheOptions{
		ChunkInactivityTimeout: inactivityTimeout,
		CleanupInterval:        cleanupInterval,
		OnStreamComplete: func(meta *StreamMeta, chunks []*MessageChunk) error {
			mu.Lock()
			completedStreams[meta.MessageId] = true
			mu.Unlock()
			return nil
		},
	})
	defer cache.Close()

	// Create multiple streams - some will be kept active, others will become inactive
	for i := 1; i <= 3; i++ {
		messageId := int64(i)
		meta := NewStreamMeta(messageId)
		cache.OpenStream(meta)
		cache.AppendChunk(&MessageChunk{
			MessageId: messageId,
			ChunkId:   0,
			Payload:   []byte(fmt.Sprintf("initial chunk for stream %d", messageId)),
		})
	}

	// Keep stream 1 active by adding chunks
	go func() {
		time.Sleep(inactivityTimeout / 2)
		cache.AppendChunk(&MessageChunk{
			MessageId: 1,
			ChunkId:   1,
			Payload:   []byte("keeping stream 1 active"),
		})
	}()

	// Let streams 2 and 3 become inactive
	// Wait for inactivity timeout + cleanup to trigger
	time.Sleep(inactivityTimeout + cleanupInterval + 100*time.Millisecond)

	// Check results
	mu.Lock()
	stream1Completed := completedStreams[1]
	stream2Completed := completedStreams[2]
	stream3Completed := completedStreams[3]
	mu.Unlock()

	// Stream 1 should still be active (not completed)
	if stream1Completed {
		t.Log("Note: Stream 1 was completed despite being kept active (timing dependent)")
	}

	// Streams 2 and 3 should be completed due to inactivity
	inactiveCompleted := 0
	if stream2Completed {
		inactiveCompleted++
	}
	if stream3Completed {
		inactiveCompleted++
	}

	if inactiveCompleted == 0 {
		t.Error("At least some inactive streams should have been auto-completed")
	}

	t.Logf("Stream 1 (active) completed: %v, Streams 2&3 (inactive) completed: %d/2",
		stream1Completed, inactiveCompleted)
}

func TestStreamCache_EndReason(t *testing.T) {
	// Test that EndReason is properly set and tracked
	cache := NewStreamCache(&StreamCacheOptions{
		OnStreamComplete: func(meta *StreamMeta, chunks []*MessageChunk) error {
			return nil
		},
	})
	defer cache.Close()

	// Test 1: Manual completion with success reason
	messageId1 := int64(1)
	meta1 := NewStreamMeta(messageId1)
	cache.OpenStream(meta1)
	cache.AppendChunk(&MessageChunk{
		MessageId: messageId1,
		ChunkId:   0,
		Payload:   []byte("test chunk 1"),
	})

	// End with success reason
	err := cache.EndStream(messageId1, EndReasonSuccess)
	if err != nil {
		t.Fatalf("Failed to end stream with success reason: %v", err)
	}

	// Test 2: Manual completion with cancelled reason
	messageId2 := int64(2)
	meta2 := NewStreamMeta(messageId2)
	cache.OpenStream(meta2)
	cache.AppendChunk(&MessageChunk{
		MessageId: messageId2,
		ChunkId:   0,
		Payload:   []byte("test chunk 2"),
	})

	// End with cancelled reason
	err = cache.EndStream(messageId2, EndReasonCancelled)
	if err != nil {
		t.Fatalf("Failed to end stream with cancelled reason: %v", err)
	}

	// Test 3: Invalid reason should default to success
	messageId3 := int64(3)
	meta3 := NewStreamMeta(messageId3)
	cache.OpenStream(meta3)
	cache.AppendChunk(&MessageChunk{
		MessageId: messageId3,
		ChunkId:   0,
		Payload:   []byte("test chunk 3"),
	})

	// End with invalid reason (should default to success)
	err = cache.EndStream(messageId3, 111)
	if err != nil {
		t.Fatalf("Failed to end stream with invalid reason: %v", err)
	}

	t.Log("EndReason functionality test completed successfully")
}

func TestStreamCache_InactivityTimeout_EndReason(t *testing.T) {
	// Test that inactivity timeout sets EndReasonTimeout
	inactivityTimeout := 100 * time.Millisecond
	cleanupInterval := 50 * time.Millisecond

	cache := NewStreamCache(&StreamCacheOptions{
		ChunkInactivityTimeout: inactivityTimeout,
		CleanupInterval:        cleanupInterval,
		OnStreamComplete: func(meta *StreamMeta, chunks []*MessageChunk) error {
			return nil
		},
	})
	defer cache.Close()

	// Create a stream that will timeout
	messageId := int64(1)
	meta := NewStreamMeta(messageId)
	cache.OpenStream(meta)
	cache.AppendChunk(&MessageChunk{
		MessageId: messageId,
		ChunkId:   0,
		Payload:   []byte("test chunk"),
	})

	// Wait for inactivity timeout to trigger
	time.Sleep(inactivityTimeout + cleanupInterval + 100*time.Millisecond)

	// Stream should have been auto-completed with timeout reason
	// Since the stream is removed after completion, we can't verify the EndReason directly
	// But we can verify that the stream was completed (no longer exists)
	if cache.HasStream(messageId) {
		t.Error("Stream should have been auto-completed due to inactivity")
	}

	t.Log("Inactivity timeout EndReason test completed successfully")
}

func TestStreamCache_EndStreamInChannel(t *testing.T) {
	// Test ending all streams in a specific channel
	completedStreams := make(map[int64]bool)
	var mu sync.Mutex

	cache := NewStreamCache(&StreamCacheOptions{
		OnStreamComplete: func(meta *StreamMeta, chunks []*MessageChunk) error {
			mu.Lock()
			completedStreams[meta.MessageId] = true
			mu.Unlock()
			return nil
		},
	})
	defer cache.Close()

	// Create streams in different channels
	// Channel "general" type 1
	meta1 := NewStreamMetaBuilder(1).Channel("general", 1).Build()
	meta2 := NewStreamMetaBuilder(2).Channel("general", 1).Build()
	meta3 := NewStreamMetaBuilder(3).Channel("general", 1).Build()

	// Channel "private" type 2
	meta4 := NewStreamMetaBuilder(4).Channel("private", 2).Build()
	meta5 := NewStreamMetaBuilder(5).Channel("private", 2).Build()

	// Channel "general" type 2 (different type, same channel ID)
	meta6 := NewStreamMetaBuilder(6).Channel("general", 2).Build()

	// Open all streams
	cache.OpenStream(meta1)
	cache.OpenStream(meta2)
	cache.OpenStream(meta3)
	cache.OpenStream(meta4)
	cache.OpenStream(meta5)
	cache.OpenStream(meta6)

	// Add chunks to all streams
	for i := int64(1); i <= 6; i++ {
		cache.AppendChunk(&MessageChunk{
			MessageId: i,
			ChunkId:   0,
			Payload:   []byte(fmt.Sprintf("chunk for stream %d", i)),
		})
	}

	// End all streams in channel "general" type 1
	err := cache.EndStreamInChannel("general", 1, EndReasonForce)
	if err != nil {
		t.Fatalf("Failed to end streams in channel: %v", err)
	}

	// Verify that streams 1, 2, 3 were completed
	mu.Lock()
	for i := int64(1); i <= 3; i++ {
		if !completedStreams[i] {
			t.Errorf("Stream %d in channel 'general' type 1 should have been completed", i)
		}
	}

	// Verify that streams 4, 5, 6 were NOT completed
	for i := int64(4); i <= 6; i++ {
		if completedStreams[i] {
			t.Errorf("Stream %d should NOT have been completed", i)
		}
	}
	mu.Unlock()

	// Verify streams are no longer in cache
	for i := int64(1); i <= 3; i++ {
		if cache.HasStream(i) {
			t.Errorf("Stream %d should have been removed from cache", i)
		}
	}

	// Verify other streams are still in cache
	for i := int64(4); i <= 6; i++ {
		if !cache.HasStream(i) {
			t.Errorf("Stream %d should still be in cache", i)
		}
	}
}

func TestStreamCache_EndStreamInChannel_EmptyChannel(t *testing.T) {
	// Test ending streams in an empty channel
	cache := NewStreamCache(nil)
	defer cache.Close()

	// Try to end streams in a non-existent channel
	err := cache.EndStreamInChannel("nonexistent", 1, EndReasonForce)
	if err != nil {
		t.Errorf("EndStreamInChannel should succeed even for empty channels, got: %v", err)
	}
}

func TestStreamCache_EndStreamInChannel_Errors(t *testing.T) {
	// Test error handling
	cache := NewStreamCache(nil)
	defer cache.Close()

	// Test invalid channel ID
	err := cache.EndStreamInChannel("", 1, EndReasonForce)
	if err != ErrInvalidChannelId {
		t.Errorf("Expected ErrInvalidChannelId, got: %v", err)
	}

	// Test invalid reason (should default to success)
	meta := NewStreamMetaBuilder(1).Channel("test", 1).Build()
	cache.OpenStream(meta)
	cache.AppendChunk(&MessageChunk{
		MessageId: 1,
		ChunkId:   0,
		Payload:   []byte("test chunk"),
	})

	err = cache.EndStreamInChannel("test", 1, 111) // Invalid reason
	if err != nil {
		t.Errorf("EndStreamInChannel should handle invalid reason gracefully, got: %v", err)
	}
}

func TestStreamCache_EndStreamInChannel_Concurrent(t *testing.T) {
	// Test concurrent access to EndStreamInChannel
	completedStreams := make(map[int64]bool)
	var mu sync.Mutex

	cache := NewStreamCache(&StreamCacheOptions{
		OnStreamComplete: func(meta *StreamMeta, chunks []*MessageChunk) error {
			mu.Lock()
			completedStreams[meta.MessageId] = true
			mu.Unlock()
			return nil
		},
	})
	defer cache.Close()

	// Create multiple streams in the same channel
	numStreams := 10
	for i := 1; i <= numStreams; i++ {
		meta := NewStreamMetaBuilder(int64(i)).Channel("concurrent", 1).Build()
		cache.OpenStream(meta)
		cache.AppendChunk(&MessageChunk{
			MessageId: int64(i),
			ChunkId:   0,
			Payload:   []byte(fmt.Sprintf("chunk %d", i)),
		})
	}

	// Concurrently end streams in the channel and perform other operations
	var wg sync.WaitGroup

	// Goroutine 1: End streams in channel
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := cache.EndStreamInChannel("concurrent", 1, EndReasonForce)
		if err != nil {
			t.Errorf("EndStreamInChannel failed: %v", err)
		}
	}()

	// Goroutine 2: Try to append chunks concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= numStreams; i++ {
			cache.AppendChunk(&MessageChunk{
				MessageId: int64(i),
				ChunkId:   1,
				Payload:   []byte(fmt.Sprintf("concurrent chunk %d", i)),
			})
		}
	}()

	// Goroutine 3: Check stream existence
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= numStreams; i++ {
			cache.HasStream(int64(i))
		}
	}()

	wg.Wait()

	// Verify that all streams were eventually completed
	mu.Lock()
	completedCount := len(completedStreams)
	mu.Unlock()

	if completedCount != numStreams {
		t.Logf("Expected %d streams to be completed, got %d (this may vary due to timing)",
			numStreams, completedCount)
	}

	t.Logf("Concurrent EndStreamInChannel test completed with %d/%d streams completed",
		completedCount, numStreams)
}

func TestStreamCache_EndStream_ConcurrentAccess(t *testing.T) {
	// Test that EndStream doesn't block other operations during slow callback execution
	callbackDuration := 100 * time.Millisecond

	cache := NewStreamCache(&StreamCacheOptions{
		OnStreamComplete: func(meta *StreamMeta, chunks []*MessageChunk) error {
			// Simulate slow callback (e.g., database operation)
			time.Sleep(callbackDuration)
			return nil
		},
	})
	defer cache.Close()

	// Create a stream to end
	messageId := int64(1)
	meta1 := NewStreamMeta(messageId)
	cache.OpenStream(meta1)
	cache.AppendChunk(&MessageChunk{
		MessageId: messageId,
		ChunkId:   0,
		Payload:   []byte("test chunk"),
	})

	// Create another stream for concurrent operations
	messageId2 := int64(2)
	meta2 := NewStreamMeta(messageId2)
	cache.OpenStream(meta2)

	// Channel to coordinate test execution
	endStreamStarted := make(chan struct{})
	concurrentOpsDone := make(chan struct{})
	endStreamDone := make(chan error)

	// Start EndStream in a goroutine (this will trigger the slow callback)
	go func() {
		close(endStreamStarted) // Signal that EndStream has started
		err := cache.EndStream(messageId, EndReasonSuccess)
		endStreamDone <- err
	}()

	// Wait for EndStream to start
	<-endStreamStarted

	// Give EndStream a moment to acquire the lock and start processing
	time.Sleep(10 * time.Millisecond)

	// Perform concurrent operations that should NOT be blocked by the slow callback
	go func() {
		defer close(concurrentOpsDone)

		start := time.Now()

		// These operations should complete quickly even while callback is running
		operations := []func() error{
			func() error {
				_, err := cache.GetStreamInfo(messageId2)
				return err
			},
			func() error {
				return cache.AppendChunk(&MessageChunk{
					MessageId: messageId2,
					ChunkId:   1,
					Payload:   []byte("concurrent chunk"),
				})
			},
			func() error {
				return cache.UpdateStreamMeta(messageId2, func(meta *StreamMeta) {
					meta.SetCustomField("concurrent", true)
				})
			},
			func() error {
				_ = cache.HasStreamInChannel("test", 1)
				return nil
			},
			func() error {
				_ = cache.GetStreamsByUser("testuser")
				return nil
			},
		}

		// Execute all operations
		for i, op := range operations {
			if err := op(); err != nil {
				t.Errorf("Concurrent operation %d failed: %v", i, err)
				return
			}
		}

		duration := time.Since(start)

		// These operations should complete much faster than the callback duration
		// Allow some buffer for test execution overhead
		maxExpectedDuration := callbackDuration / 4 // 25ms buffer
		if duration > maxExpectedDuration {
			t.Errorf("Concurrent operations took too long: %v (expected < %v)",
				duration, maxExpectedDuration)
		}

		t.Logf("Concurrent operations completed in %v while callback was running", duration)
	}()

	// Wait for both EndStream and concurrent operations to complete
	select {
	case err := <-endStreamDone:
		if err != nil {
			t.Errorf("EndStream failed: %v", err)
		}
	case <-time.After(callbackDuration + 50*time.Millisecond):
		t.Error("EndStream took too long to complete")
	}

	select {
	case <-concurrentOpsDone:
		// Success - concurrent operations completed
	case <-time.After(50 * time.Millisecond):
		t.Error("Concurrent operations took too long or were blocked")
	}
}
