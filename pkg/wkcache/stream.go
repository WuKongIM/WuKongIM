package wkcache

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	uatomic "go.uber.org/atomic"
	"go.uber.org/zap"
)

// Constants for stream cache configuration
const (
	// DefaultMaxMemorySize is the default maximum memory size for the cache (100MB)
	DefaultMaxMemorySize = 100 * 1024 * 1024
	// DefaultMaxStreams is the default maximum number of concurrent streams
	DefaultMaxStreams = 10000
	// DefaultMaxChunksPerStream is the default maximum chunks per stream
	DefaultMaxChunksPerStream = 1000
	// DefaultStreamTimeout is the default timeout for inactive streams (30 minutes)
	DefaultStreamTimeout = 30 * time.Minute
	// DefaultChunkInactivityTimeout is the default timeout for auto-completing inactive streams (30 seconds)
	DefaultChunkInactivityTimeout = 30 * time.Second
	// CleanupInterval is the interval for cleanup operations
	CleanupInterval = 5 * time.Minute
)

// EndReason constants define why a stream was completed
const (
	// EndReasonSuccess indicates the stream completed successfully (default)
	EndReasonSuccess = 0
	// EndReasonTimeout indicates the stream ended due to inactivity timeout
	EndReasonTimeout = 1
	// EndReasonError indicates the stream ended due to an error
	EndReasonError = 2
	// EndReasonCancelled indicates the stream was manually cancelled
	EndReasonCancelled = 3
	// EndReasonForce indicates the stream was forcefully ended (e.g., channel closure)
	EndReasonForce = 4
)

// Errors
var (
	ErrStreamNotFound     = errors.New("stream not found")
	ErrStreamClosed       = errors.New("stream is closed")
	ErrMemoryLimitReached = errors.New("memory limit reached")
	ErrTooManyStreams     = errors.New("too many concurrent streams")
	ErrTooManyChunks      = errors.New("too many chunks in stream")
	ErrInvalidClientMsgNo = errors.New("invalid client message number")
	ErrInvalidChunkId     = errors.New("invalid chunk id")
	ErrDuplicateChunk     = errors.New("duplicate chunk id")
	ErrInvalidChannelId   = errors.New("invalid channel id")
)

// MessageChunk represents a single chunk of a streaming message
type MessageChunk struct {
	ClientMsgNo string // Client message number
	ChunkId     uint64 // Chunk ID within the message
	Payload     []byte // Chunk payload data
}

// Size returns the memory size of the chunk
func (c *MessageChunk) Size() int {
	return len(c.ClientMsgNo) + 8 + len(c.Payload) // ClientMsgNo string + 8 bytes for ChunkId + payload size
}

// StreamMeta contains metadata for a streaming message
type StreamMeta struct {
	// Core fields
	ClientMsgNo string    // Client-side message number (primary key)
	CreatedAt   time.Time // Creation timestamp
	UpdatedAt   time.Time // Last update timestamp
	Closed      bool      // Whether the stream is closed
	EndReason   uint8     // Reason why the stream was completed (EndReasonSuccess, EndReasonTimeout, etc.)

	// Common streaming message fields
	ChannelId   string // fakeChannelID Channel/conversation this stream belongs to, fakeChannelID
	ChannelType uint8  // Type of channel (person, group, etc.)
	FromUid     string // Sender user ID
	Seq         uatomic.Uint64
	MessageId   int64 // Message identifier

	// Custom metadata fields for extensibility
	CustomFields map[string]interface{} // Flexible storage for additional metadata
}

// NewStreamMeta creates a new StreamMeta with initialized custom fields
func NewStreamMeta(clientMsgNo string) *StreamMeta {
	now := time.Now()
	return &StreamMeta{
		ClientMsgNo:  clientMsgNo,
		CreatedAt:    now,
		UpdatedAt:    now,
		Closed:       false,
		EndReason:    EndReasonSuccess, // Default to success
		CustomFields: make(map[string]interface{}),
	}
}

// SetCustomField sets a custom metadata field
func (sm *StreamMeta) SetCustomField(key string, value interface{}) {
	if sm.CustomFields == nil {
		sm.CustomFields = make(map[string]interface{})
	}
	sm.CustomFields[key] = value
	sm.UpdatedAt = time.Now()
}

// GetCustomField gets a custom metadata field
func (sm *StreamMeta) GetCustomField(key string) (interface{}, bool) {
	if sm.CustomFields == nil {
		return nil, false
	}
	value, exists := sm.CustomFields[key]
	return value, exists
}

// GetCustomFieldString gets a custom field as string
func (sm *StreamMeta) GetCustomFieldString(key string) (string, bool) {
	if value, exists := sm.GetCustomField(key); exists {
		if str, ok := value.(string); ok {
			return str, true
		}
	}
	return "", false
}

// GetCustomFieldInt gets a custom field as int
func (sm *StreamMeta) GetCustomFieldInt(key string) (int, bool) {
	if value, exists := sm.GetCustomField(key); exists {
		if i, ok := value.(int); ok {
			return i, true
		}
	}
	return 0, false
}

// GetCustomFieldBool gets a custom field as bool
func (sm *StreamMeta) GetCustomFieldBool(key string) (bool, bool) {
	if value, exists := sm.GetCustomField(key); exists {
		if b, ok := value.(bool); ok {
			return b, true
		}
	}
	return false, false
}

// RemoveCustomField removes a custom metadata field
func (sm *StreamMeta) RemoveCustomField(key string) {
	if sm.CustomFields != nil {
		delete(sm.CustomFields, key)
		sm.UpdatedAt = time.Now()
	}
}

func (sm *StreamMeta) GetSeq() uint64 {
	return sm.Seq.Load()
}

func (sm *StreamMeta) NextSeq() uint64 {
	return sm.Seq.Inc()
}

// Clone creates a deep copy of the StreamMeta
func (sm *StreamMeta) Clone() *StreamMeta {
	clone := &StreamMeta{
		ClientMsgNo:  sm.ClientMsgNo,
		CreatedAt:    sm.CreatedAt,
		UpdatedAt:    sm.UpdatedAt,
		Closed:       sm.Closed,
		EndReason:    sm.EndReason,
		ChannelId:    sm.ChannelId,
		ChannelType:  sm.ChannelType,
		FromUid:      sm.FromUid,
		MessageId:    sm.MessageId,
		CustomFields: make(map[string]interface{}),
	}

	// Deep copy custom fields
	if sm.CustomFields != nil {
		for k, v := range sm.CustomFields {
			clone.CustomFields[k] = v
		}
	}

	return clone
}

// StreamMetaBuilder helps build StreamMeta with fluent API
type StreamMetaBuilder struct {
	meta *StreamMeta
}

// NewStreamMetaBuilder creates a new builder for StreamMeta
func NewStreamMetaBuilder(clientMsgNo string) *StreamMetaBuilder {
	return &StreamMetaBuilder{
		meta: NewStreamMeta(clientMsgNo),
	}
}

// Channel sets the channel information
func (b *StreamMetaBuilder) Channel(channelId string, channelType uint8) *StreamMetaBuilder {
	b.meta.ChannelId = channelId
	b.meta.ChannelType = channelType
	return b
}

// From sets the sender information
func (b *StreamMetaBuilder) From(fromUid string) *StreamMetaBuilder {
	b.meta.FromUid = fromUid
	return b
}

// ClientMessage sets the client message information
func (b *StreamMetaBuilder) ClientMessage(clientMsgNo string) *StreamMetaBuilder {
	b.meta.ClientMsgNo = clientMsgNo
	return b
}

func (b *StreamMetaBuilder) MessageId(messageId int64) *StreamMetaBuilder {
	b.meta.MessageId = messageId
	return b
}

// CustomField sets a custom field
func (b *StreamMetaBuilder) CustomField(key string, value interface{}) *StreamMetaBuilder {
	b.meta.SetCustomField(key, value)
	return b
}

// Build returns the constructed StreamMeta
func (b *StreamMetaBuilder) Build() *StreamMeta {
	return b.meta.Clone()
}

// StreamData holds the complete data for a stream
type StreamData struct {
	Meta   *StreamMeta              // Stream metadata
	Chunks map[uint64]*MessageChunk // Chunks indexed by ChunkId
	Size   int64                    // Total memory size of this stream
}

// StreamCache is a high-performance cache for streaming message chunks
type StreamCache struct {
	// Configuration
	maxMemorySize          int64         // Maximum memory usage in bytes
	maxStreams             int           // Maximum number of concurrent streams
	maxChunksPerStream     int           // Maximum chunks per stream
	streamTimeout          time.Duration // Timeout for inactive streams
	chunkInactivityTimeout time.Duration // Timeout for auto-completing inactive streams
	cleanupInterval        time.Duration // Interval for cleanup operations

	// State
	streams     map[string]*StreamData // Active streams indexed by ClientMsgNo
	memoryUsage int64                  // Current memory usage in bytes
	streamCount int32                  // Current number of streams

	// Synchronization
	mu sync.RWMutex // Main mutex for thread safety

	// Cleanup
	stopCleanup chan struct{} // Channel to stop cleanup goroutine
	cleanupDone chan struct{} // Channel to signal cleanup completion

	// Logging
	wklog.Log

	// Callbacks
	onStreamComplete func(meta *StreamMeta, chunks []*MessageChunk) error // Called when stream is complete
}

// StreamCacheOptions contains configuration options for StreamCache
type StreamCacheOptions struct {
	MaxMemorySize          int64                                                // Maximum memory usage in bytes
	MaxStreams             int                                                  // Maximum number of concurrent streams
	MaxChunksPerStream     int                                                  // Maximum chunks per stream
	StreamTimeout          time.Duration                                        // Timeout for inactive streams
	ChunkInactivityTimeout time.Duration                                        // Timeout for auto-completing inactive streams
	CleanupInterval        time.Duration                                        // Interval for cleanup operations
	OnStreamComplete       func(meta *StreamMeta, chunks []*MessageChunk) error // Callback when stream is complete
}

// NewStreamCache creates a new StreamCache with the given options
func NewStreamCache(opts *StreamCacheOptions) *StreamCache {
	if opts == nil {
		opts = &StreamCacheOptions{}
	}

	// Set defaults
	if opts.MaxMemorySize <= 0 {
		opts.MaxMemorySize = DefaultMaxMemorySize
	}
	if opts.MaxStreams <= 0 {
		opts.MaxStreams = DefaultMaxStreams
	}
	if opts.MaxChunksPerStream <= 0 {
		opts.MaxChunksPerStream = DefaultMaxChunksPerStream
	}
	if opts.StreamTimeout <= 0 {
		opts.StreamTimeout = DefaultStreamTimeout
	}
	if opts.ChunkInactivityTimeout <= 0 {
		opts.ChunkInactivityTimeout = DefaultChunkInactivityTimeout
	}
	if opts.CleanupInterval <= 0 {
		opts.CleanupInterval = CleanupInterval
	}

	cache := &StreamCache{
		maxMemorySize:          opts.MaxMemorySize,
		maxStreams:             opts.MaxStreams,
		maxChunksPerStream:     opts.MaxChunksPerStream,
		streamTimeout:          opts.StreamTimeout,
		chunkInactivityTimeout: opts.ChunkInactivityTimeout,
		cleanupInterval:        opts.CleanupInterval,
		streams:                make(map[string]*StreamData),
		memoryUsage:            0,
		streamCount:            0,
		stopCleanup:            make(chan struct{}),
		cleanupDone:            make(chan struct{}),
		Log:                    wklog.NewWKLog("StreamCache"),
		onStreamComplete:       opts.OnStreamComplete,
	}

	// Start cleanup goroutine
	go cache.cleanupLoop()

	return cache
}

// Close stops the cache and cleans up resources
func (sc *StreamCache) Close() error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	// Stop cleanup goroutine
	close(sc.stopCleanup)

	// Wait for cleanup to finish
	<-sc.cleanupDone

	// Clear all streams
	sc.streams = make(map[string]*StreamData)
	sc.memoryUsage = 0
	sc.streamCount = 0

	sc.Info("StreamCache closed")
	return nil
}

// OpenStream opens a new stream with comprehensive metadata
func (sc *StreamCache) OpenStream(meta *StreamMeta) error {
	if meta == nil {
		return errors.New("stream meta cannot be nil")
	}
	if meta.ClientMsgNo == "" {
		return ErrInvalidClientMsgNo
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	// Check if we have too many streams
	if len(sc.streams) >= sc.maxStreams {
		return ErrTooManyStreams
	}

	now := time.Now()

	// Check if stream already exists
	if stream, exists := sc.streams[meta.ClientMsgNo]; exists {
		// Update existing stream metadata
		oldMeta := stream.Meta
		stream.Meta = meta.Clone()
		stream.Meta.CreatedAt = oldMeta.CreatedAt // Preserve creation time
		stream.Meta.UpdatedAt = now

		sc.Debug("Updated advanced stream metadata",
			zap.String("clientMsgNo", meta.ClientMsgNo),
			zap.String("channelId", meta.ChannelId),
			zap.String("fromUid", meta.FromUid))
		return nil
	}

	// Create new stream with provided metadata
	streamMeta := meta.Clone()
	streamMeta.CreatedAt = now
	streamMeta.UpdatedAt = now

	stream := &StreamData{
		Meta:   streamMeta,
		Chunks: make(map[uint64]*MessageChunk),
		Size:   0,
	}

	sc.streams[meta.ClientMsgNo] = stream
	sc.streamCount++

	sc.Debug("Created new advanced stream",
		zap.String("clientMsgNo", meta.ClientMsgNo),
		zap.String("channelId", meta.ChannelId),
		zap.String("fromUid", meta.FromUid),
		zap.Int32("streamCount", sc.streamCount))

	return nil
}

// AppendChunk appends a message chunk to the cache
func (sc *StreamCache) AppendChunk(chunk *MessageChunk) error {
	if chunk == nil {
		return errors.New("chunk cannot be nil")
	}
	if chunk.ClientMsgNo == "" {
		return ErrInvalidClientMsgNo
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	// Get existing stream
	stream, exists := sc.streams[chunk.ClientMsgNo]
	if !exists {
		return ErrStreamNotFound
	}

	// Check if stream is closed
	if stream.Meta.Closed {
		return ErrStreamClosed
	}

	// Check chunk limit per stream
	if len(stream.Chunks) >= sc.maxChunksPerStream {
		return ErrTooManyChunks
	}

	// Check for duplicate chunk
	if _, exists := stream.Chunks[chunk.ChunkId]; exists {
		return ErrDuplicateChunk
	}

	// Calculate memory impact
	chunkSize := int64(chunk.Size())
	newMemoryUsage := atomic.LoadInt64(&sc.memoryUsage) + chunkSize

	// Check memory limit
	if newMemoryUsage > sc.maxMemorySize {
		return ErrMemoryLimitReached
	}

	// Add chunk to stream
	stream.Chunks[chunk.ChunkId] = chunk
	stream.Size += chunkSize
	stream.Meta.UpdatedAt = time.Now()

	// Update global memory usage
	atomic.AddInt64(&sc.memoryUsage, chunkSize)

	sc.Debug("Appended chunk",
		zap.String("clientMsgNo", chunk.ClientMsgNo),
		zap.Uint64("chunkId", chunk.ChunkId),
		zap.Int("chunkSize", len(chunk.Payload)),
		zap.Int("totalChunks", len(stream.Chunks)),
		zap.Int64("streamSize", stream.Size))

	// Note: Streams only complete via manual EndStream() calls
	// isStreamComplete() always returns false, so no automatic completion
	return nil
}

// EndStream marks a stream as complete and flushes it
func (sc *StreamCache) EndStream(clientMsgNo string, reason uint8) error {
	if clientMsgNo == "" {
		return ErrInvalidClientMsgNo
	}

	// Default to success if reason is 0 or invalid
	if reason < EndReasonSuccess || reason > EndReasonForce {
		reason = EndReasonSuccess
	}

	// Critical section: acquire lock, validate, and remove stream from cache
	var streamData *StreamData
	var chunks []*MessageChunk

	func() {
		sc.mu.Lock()
		defer sc.mu.Unlock()

		stream, exists := sc.streams[clientMsgNo]
		if !exists {
			return // Will be handled after the critical section
		}

		if stream.Meta.Closed {
			return // Will be handled after the critical section
		}

		// Mark stream as closed with the specified reason
		stream.Meta.Closed = true
		stream.Meta.EndReason = reason
		stream.Meta.UpdatedAt = time.Now()

		sc.Debug("Ending stream",
			zap.String("clientMsgNo", clientMsgNo),
			zap.Int("totalChunks", len(stream.Chunks)))

		// Prepare data for callback execution outside the lock
		streamData = stream
		chunks = sc.PrepareChunksForCallback(stream)

		// Remove stream from cache immediately while holding the lock
		delete(sc.streams, clientMsgNo)
		atomic.AddInt64(&sc.memoryUsage, -stream.Size)
		atomic.AddInt32(&sc.streamCount, -1)

		sc.Debug("Removed completed stream from cache",
			zap.String("clientMsgNo", clientMsgNo),
			zap.Int64("freedMemory", stream.Size))
	}()

	// Handle validation errors after releasing the lock
	if streamData == nil {
		stream, exists := sc.streams[clientMsgNo]
		if !exists {
			return ErrStreamNotFound
		}
		if stream.Meta.Closed {
			return ErrStreamClosed
		}
		// This shouldn't happen, but handle it gracefully
		return errors.New("unexpected error during stream completion")
	}

	// Execute callback outside the critical section
	return sc.executeStreamCompletionCallback(streamData.Meta, chunks, streamData.Size)
}

// PrepareChunksForCallback converts chunks map to sorted slice for callback execution
func (sc *StreamCache) PrepareChunksForCallback(stream *StreamData) []*MessageChunk {
	// Convert chunks map to sorted slice
	chunks := make([]*MessageChunk, 0, len(stream.Chunks))
	for _, chunk := range stream.Chunks {
		chunks = append(chunks, chunk)
	}

	// Sort chunks by ChunkId
	for i := 0; i < len(chunks)-1; i++ {
		for j := i + 1; j < len(chunks); j++ {
			if chunks[i].ChunkId > chunks[j].ChunkId {
				chunks[i], chunks[j] = chunks[j], chunks[i]
			}
		}
	}

	return chunks
}

func (sc *StreamCache) GetStreamData(stream *StreamData) []byte {
	chunks := sc.PrepareChunksForCallback(stream)
	return sc.mergeChunks(chunks)
}

func (sc *StreamCache) mergeChunks(chunks []*MessageChunk) []byte {
	totalSize := 0
	for _, chunk := range chunks {
		totalSize += len(chunk.Payload)
	}

	mergedData := make([]byte, 0, totalSize)
	for _, chunk := range chunks {
		mergedData = append(mergedData, chunk.Payload...)
	}

	return mergedData
}

// executeStreamCompletionCallback executes the completion callback outside the critical section
func (sc *StreamCache) executeStreamCompletionCallback(meta *StreamMeta, chunks []*MessageChunk, streamSize int64) error {
	sc.Info("Completing stream",
		zap.String("clientMsgNo", meta.ClientMsgNo),
		zap.Int("chunkCount", len(chunks)),
		zap.Int64("streamSize", streamSize))

	// Call completion callback if provided
	var callbackErr error
	if sc.onStreamComplete != nil {
		callbackErr = sc.onStreamComplete(meta, chunks)
		if callbackErr != nil {
			sc.Error("Stream completion callback failed",
				zap.String("clientMsgNo", meta.ClientMsgNo),
				zap.Error(callbackErr))
		}
	}

	return callbackErr
}

// EndStreamInChannel ends all active streams within a specific channel
func (sc *StreamCache) EndStreamInChannel(channelId string, channelType uint8, reason uint8) error {
	if channelId == "" {
		return ErrInvalidChannelId
	}

	// Default to success if reason is 0 or invalid
	if reason < EndReasonSuccess || reason > EndReasonForce {
		reason = EndReasonSuccess
	}

	// First pass: identify streams in the channel (read lock only)
	streamsToEnd := make([]string, 0)

	sc.mu.RLock()
	for clientMsgNo, stream := range sc.streams {
		// Skip already closed streams
		if stream.Meta.Closed {
			continue
		}

		// Check if stream matches the channel
		if stream.Meta.ChannelId == channelId && stream.Meta.ChannelType == channelType {
			streamsToEnd = append(streamsToEnd, clientMsgNo)
		}
	}
	sc.mu.RUnlock()

	// No streams found in the channel
	if len(streamsToEnd) == 0 {
		sc.Debug("No active streams found in channel",
			zap.String("channelId", channelId),
			zap.Uint8("channelType", channelType))
		return nil
	}

	sc.Info("Ending streams in channel",
		zap.String("channelId", channelId),
		zap.Uint8("channelType", channelType),
		zap.Int("streamCount", len(streamsToEnd)),
		zap.Uint8("reason", reason))

	// Second pass: end each stream (without holding locks during EndStream)
	var errors []error
	successCount := 0

	for _, clientMsgNo := range streamsToEnd {
		err := sc.EndStream(clientMsgNo, reason)
		if err != nil {
			// Stream might have been manually ended or removed in the meantime
			if err != ErrStreamNotFound && err != ErrStreamClosed {
				errors = append(errors, fmt.Errorf("failed to end stream %s: %w", clientMsgNo, err))
				sc.Error("Failed to end stream in channel",
					zap.String("clientMsgNo", clientMsgNo),
					zap.String("channelId", channelId),
					zap.Uint8("channelType", channelType),
					zap.Error(err))
			}
		} else {
			successCount++
		}
	}

	sc.Info("Completed ending streams in channel",
		zap.String("channelId", channelId),
		zap.Uint8("channelType", channelType),
		zap.Int("totalStreams", len(streamsToEnd)),
		zap.Int("successCount", successCount),
		zap.Int("errorCount", len(errors)))

	// Return combined error if any individual EndStream calls failed
	if len(errors) > 0 {
		return fmt.Errorf("failed to end %d streams in channel: %v", len(errors), errors)
	}

	return nil
}

// GetStreamInfo returns information about a stream
func (sc *StreamCache) GetStreamInfo(clientMsgNo string) (*StreamMeta, error) {
	if clientMsgNo == "" {
		return nil, ErrInvalidClientMsgNo
	}

	sc.mu.RLock()
	defer sc.mu.RUnlock()

	stream, exists := sc.streams[clientMsgNo]
	if !exists {
		return nil, ErrStreamNotFound
	}

	// Return a deep copy of the metadata
	return stream.Meta, nil
}

func (sc *StreamCache) GetStream(clientMsgNo string) (*StreamData, error) {
	if clientMsgNo == "" {
		return nil, ErrInvalidClientMsgNo
	}

	sc.mu.RLock()
	defer sc.mu.RUnlock()

	stream, exists := sc.streams[clientMsgNo]
	if !exists {
		return nil, ErrStreamNotFound
	}
	return stream, nil
}

// GetStats returns cache statistics
func (sc *StreamCache) GetStats() map[string]interface{} {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	return map[string]interface{}{
		"stream_count":   len(sc.streams),
		"memory_usage":   atomic.LoadInt64(&sc.memoryUsage),
		"max_memory":     sc.maxMemorySize,
		"max_streams":    sc.maxStreams,
		"memory_percent": float64(atomic.LoadInt64(&sc.memoryUsage)) / float64(sc.maxMemorySize) * 100,
	}
}

// cleanupLoop runs periodic cleanup of expired streams
func (sc *StreamCache) cleanupLoop() {
	defer close(sc.cleanupDone)

	ticker := time.NewTicker(sc.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sc.cleanupExpiredStreams()
			sc.autoCompleteInactiveStreams()
		case <-sc.stopCleanup:
			return
		}
	}
}

// cleanupExpiredStreams removes streams that have exceeded the timeout
func (sc *StreamCache) cleanupExpiredStreams() {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	now := time.Now()
	expiredStreams := make([]string, 0)

	for clientMsgNo, stream := range sc.streams {
		if now.Sub(stream.Meta.UpdatedAt) > sc.streamTimeout {
			expiredStreams = append(expiredStreams, clientMsgNo)
		}
	}

	if len(expiredStreams) == 0 {
		return
	}

	sc.Info("Cleaning up expired streams", zap.Int("count", len(expiredStreams)))

	for _, clientMsgNo := range expiredStreams {
		stream := sc.streams[clientMsgNo]
		delete(sc.streams, clientMsgNo)
		atomic.AddInt64(&sc.memoryUsage, -stream.Size)
		atomic.AddInt32(&sc.streamCount, -1)

		sc.Debug("Removed expired stream",
			zap.String("clientMsgNo", clientMsgNo),
			zap.Duration("age", now.Sub(stream.Meta.UpdatedAt)),
			zap.Int64("freedMemory", stream.Size))
	}
}

// autoCompleteInactiveStreams automatically completes streams that have been inactive
func (sc *StreamCache) autoCompleteInactiveStreams() {
	now := time.Now()
	inactiveStreams := make([]string, 0)

	// First pass: identify inactive streams (read lock only)
	sc.mu.RLock()
	for clientMsgNo, stream := range sc.streams {
		// Skip already closed streams
		if stream.Meta.Closed {
			continue
		}

		// Check if stream has been inactive for too long
		if now.Sub(stream.Meta.UpdatedAt) > sc.chunkInactivityTimeout {
			inactiveStreams = append(inactiveStreams, clientMsgNo)
		}
	}
	sc.mu.RUnlock()

	// No inactive streams found
	if len(inactiveStreams) == 0 {
		return
	}

	sc.Info("Auto-completing inactive streams",
		zap.Int("count", len(inactiveStreams)),
		zap.Duration("inactivityTimeout", sc.chunkInactivityTimeout))

	// Second pass: complete inactive streams (without holding locks during EndStream)
	for _, clientMsgNo := range inactiveStreams {
		// Use EndStream to trigger normal completion flow with timeout reason
		// This handles all the locking and callback execution properly
		err := sc.EndStream(clientMsgNo, EndReasonTimeout)
		if err != nil {
			// Stream might have been manually ended or removed in the meantime
			if err != ErrStreamNotFound && err != ErrStreamClosed {
				sc.Error("Failed to auto-complete inactive stream",
					zap.String("clientMsgNo", clientMsgNo),
					zap.Error(err))
			}
		} else {
			sc.Debug("Auto-completed inactive stream",
				zap.String("clientMsgNo", clientMsgNo))
		}
	}
}

// RemoveStream forcefully removes a stream from the cache
func (sc *StreamCache) RemoveStream(clientMsgNo string) error {
	if clientMsgNo == "" {
		return ErrInvalidClientMsgNo
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	stream, exists := sc.streams[clientMsgNo]
	if !exists {
		return ErrStreamNotFound
	}

	// Remove stream from cache
	delete(sc.streams, clientMsgNo)
	atomic.AddInt64(&sc.memoryUsage, -stream.Size)
	atomic.AddInt32(&sc.streamCount, -1)

	sc.Debug("Forcefully removed stream",
		zap.String("clientMsgNo", clientMsgNo),
		zap.Int64("freedMemory", stream.Size))

	return nil
}

// ListStreams returns a list of all active stream client message numbers
func (sc *StreamCache) ListStreams() []string {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	streams := make([]string, 0, len(sc.streams))
	for clientMsgNo := range sc.streams {
		streams = append(streams, clientMsgNo)
	}

	return streams
}

// GetChunkCount returns the number of chunks for a specific stream
func (sc *StreamCache) GetChunkCount(clientMsgNo string) (int, error) {
	if clientMsgNo == "" {
		return 0, ErrInvalidClientMsgNo
	}

	sc.mu.RLock()
	defer sc.mu.RUnlock()

	stream, exists := sc.streams[clientMsgNo]
	if !exists {
		return 0, ErrStreamNotFound
	}

	return len(stream.Chunks), nil
}

// HasStream checks if a stream exists in the cache
func (sc *StreamCache) HasStream(clientMsgNo string) bool {
	if clientMsgNo == "" {
		return false
	}

	sc.mu.RLock()
	defer sc.mu.RUnlock()

	_, exists := sc.streams[clientMsgNo]
	return exists
}

// GetMemoryUsage returns current memory usage in bytes
func (sc *StreamCache) GetMemoryUsage() int64 {
	return atomic.LoadInt64(&sc.memoryUsage)
}

// GetStreamCount returns the current number of active streams
func (sc *StreamCache) GetStreamCount() int32 {
	return atomic.LoadInt32(&sc.streamCount)
}

// IsMemoryLimitReached checks if memory usage is near the limit
func (sc *StreamCache) IsMemoryLimitReached(threshold float64) bool {
	if threshold <= 0 || threshold > 1 {
		threshold = 0.9 // Default to 90%
	}

	currentUsage := atomic.LoadInt64(&sc.memoryUsage)
	return float64(currentUsage) >= float64(sc.maxMemorySize)*threshold
}

// UpdateStreamMeta updates specific fields of an existing stream's metadata
func (sc *StreamCache) UpdateStreamMeta(clientMsgNo string, updateFunc func(*StreamMeta)) error {
	if clientMsgNo == "" {
		return ErrInvalidClientMsgNo
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	stream, exists := sc.streams[clientMsgNo]
	if !exists {
		return ErrStreamNotFound
	}

	if stream.Meta.Closed {
		return ErrStreamClosed
	}

	// Apply the update function
	updateFunc(stream.Meta)
	stream.Meta.UpdatedAt = time.Now()

	sc.Debug("Updated stream metadata",
		zap.String("clientMsgNo", clientMsgNo))

	return nil
}

// GetStreamsByChannel returns all streams for a specific channel
func (sc *StreamCache) GetStreamsByChannel(channelId string, channelType uint8) []*StreamMeta {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	var results []*StreamMeta
	for _, stream := range sc.streams {
		if stream.Meta.ChannelId == channelId && stream.Meta.ChannelType == channelType {
			results = append(results, stream.Meta.Clone())
		}
	}

	return results
}

// GetStreamsByUser returns all streams from a specific user
func (sc *StreamCache) GetStreamsByUser(fromUid string) []*StreamMeta {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	var results []*StreamMeta
	for _, stream := range sc.streams {
		if stream.Meta.FromUid == fromUid {
			results = append(results, stream.Meta.Clone())
		}
	}

	return results
}

// GetStreamsByCustomField returns all streams that have a specific custom field value
func (sc *StreamCache) GetStreamsByCustomField(key string, value interface{}) []*StreamMeta {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	var results []*StreamMeta
	for _, stream := range sc.streams {
		if fieldValue, exists := stream.Meta.GetCustomField(key); exists && fieldValue == value {
			results = append(results, stream.Meta.Clone())
		}
	}

	return results
}

// HasStreamInChannel efficiently checks if there are any active streams for a specific channel
// This method is optimized for high-frequency calls with minimal memory allocation and early return
func (sc *StreamCache) HasStreamInChannel(channelId string, channelType uint8) bool {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	// Early return optimization - iterate through streams and return immediately on first match
	for _, stream := range sc.streams {
		// Check if stream is active (not closed) and matches channel criteria
		if !stream.Meta.Closed &&
			stream.Meta.ChannelId == channelId &&
			stream.Meta.ChannelType == channelType {
			return true // Early return on first match for optimal performance
		}
	}

	return false
}
