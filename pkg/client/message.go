package client

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// Identity identifies one WKProto client session.
type Identity struct {
	// UID is the WuKong user id for the session.
	UID string
	// DeviceID is the stable client-side device id.
	DeviceID string
	// Token is the authentication token used by CONNECT.
	Token string
}

// Message is one outbound WKProto SEND command.
type Message struct {
	// Setting carries WKProto message flags such as no-persist and red-dot behavior.
	Setting frame.Setting
	// Expire is the server-side message expiry in seconds; zero means no expiry.
	Expire uint32
	// ClientSeq is the caller-provided sequence used to match SENDACK.
	ClientSeq uint64
	// ClientMsgNo is the caller-provided idempotency key used by the server.
	ClientMsgNo string
	// ChannelID is the target channel id.
	ChannelID string
	// ChannelType is the target channel type.
	ChannelType uint8
	// Topic is the optional message topic.
	Topic string
	// Payload is the raw message body.
	Payload []byte
}

// RoutedMessage carries a SEND command plus the sender UID used by Pool.
type RoutedMessage struct {
	// UID selects the pool session that should send the message.
	UID string
	// Message is the outbound SEND command.
	Message Message
}

// SendResult mirrors the successful or failed server SENDACK fields.
type SendResult struct {
	// ClientSeq is the client sequence echoed by the server.
	ClientSeq uint64
	// ClientMsgNo is the client message number echoed by the server.
	ClientMsgNo string
	// MessageID is the globally unique server message id.
	MessageID int64
	// MessageSeq is the channel sequence assigned by the server.
	MessageSeq uint64
	// ReasonCode is the server SENDACK reason.
	ReasonCode frame.ReasonCode
}

// SendFuture resolves when the matching SENDACK arrives or the send fails.
type SendFuture struct {
	// done receives the pending tracker's single terminal outcome.
	done <-chan sendOutcome
	// mu protects waiting, ready, completed, and outcome.
	mu sync.Mutex
	// waiting marks that one Wait caller is currently consuming done.
	waiting bool
	// ready closes when the active waiter completes or gives up.
	ready chan struct{}
	// completed marks that outcome has been cached.
	completed bool
	// outcome stores the terminal result reused by all Wait callers.
	outcome sendOutcome
}

type sendOutcome struct {
	result SendResult
	err    error
}

// Wait waits for the future result.
func (f *SendFuture) Wait(ctx context.Context) (SendResult, error) {
	if f == nil || f.done == nil {
		return SendResult{}, ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		f.mu.Lock()
		if f.completed {
			out := f.outcome
			f.mu.Unlock()
			return out.result, out.err
		}
		if !f.waiting {
			select {
			case out := <-f.done:
				f.outcome = out
				f.completed = true
				f.mu.Unlock()
				return out.result, out.err
			default:
			}

			ready := make(chan struct{})
			f.waiting = true
			f.ready = ready
			f.mu.Unlock()

			select {
			case out := <-f.done:
				f.mu.Lock()
				if !f.completed {
					f.outcome = out
					f.completed = true
				}
				if f.ready == ready {
					f.waiting = false
					close(ready)
				}
				f.mu.Unlock()
				return out.result, out.err
			case <-ctx.Done():
				f.mu.Lock()
				if !f.completed && f.ready == ready {
					f.waiting = false
					close(ready)
				}
				f.mu.Unlock()
				return SendResult{}, ctx.Err()
			}
		}
		ready := f.ready
		f.mu.Unlock()

		select {
		case <-ready:
		case <-ctx.Done():
			return SendResult{}, ctx.Err()
		}
	}
}

func (f *SendFuture) waitOnce(ctx context.Context) (SendResult, error) {
	if f == nil || f.done == nil {
		return SendResult{}, ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case out := <-f.done:
		return out.result, out.err
	case <-ctx.Done():
		return SendResult{}, ctx.Err()
	}
}
