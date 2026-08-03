package channelappend

import "errors"

var (
	// ErrInvalidSubscriberCursor reports a non-terminal subscriber page without a usable next cursor.
	ErrInvalidSubscriberCursor = errors.New("internal/channelappend: invalid subscriber cursor")
	// ErrCommitEffectFailed reports a post-commit effect failure that will be logged and dropped.
	ErrCommitEffectFailed = errors.New("internal/channelappend: commit effect failed")
	// ErrEffectPanic reports a recovered panic from an asynchronous channel append effect.
	ErrEffectPanic = errors.New("internal/channelappend: effect panic")
	// ErrRealtimeDeliveryRequired reports a transient send without an Online Delivery admission interface.
	ErrRealtimeDeliveryRequired = errors.New("internal/channelappend: realtime delivery required")
)
