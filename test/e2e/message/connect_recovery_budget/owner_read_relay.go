//go:build e2e

package connect_recovery_budget

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

const relayMaxBody = 64 << 20
const relayPresenceBody = 64 << 10

// relayObservation keeps only finite transport metadata and anonymous counters.
type relayObservation struct {
	ActiveCopies           int    `json:"active_copies"`
	FramesForwarded        uint64 `json:"frames_forwarded"`
	OwnerReadRequests      uint64 `json:"owner_read_requests"`
	RegisterRequests       uint64 `json:"register_requests"`
	CancelFrames           uint64 `json:"cancel_frames"`
	DelaysStarted          uint64 `json:"delays_started"`
	DelayedFramesForwarded uint64 `json:"delayed_frames_forwarded"`
	DelaysCanceled         uint64 `json:"delays_canceled"`
	ConfiguredDelayNanos   int64  `json:"configured_delay_nanos"`
	HoldNanos              int64  `json:"hold_nanos"`
	FromNode               uint64 `json:"from_node"`
	ToNode                 uint64 `json:"to_node"`
	BudgetMillis           uint32 `json:"budget_millis"`
	BudgetPresent          bool   `json:"budget_present"`
	FailureKind            string `json:"failure_kind,omitempty"`
}

// ownerReadRelay owns one anonymous, cancelable delay; UID bytes stay process-local.
type ownerReadRelay struct {
	mu          sync.Mutex
	needle      []byte
	delay       time.Duration
	done        chan struct{}
	doneOnce    sync.Once
	stopping    bool
	observation relayObservation
}

func newOwnerReadRelay() *ownerReadRelay { return &ownerReadRelay{done: make(chan struct{})} }

// arm starts observation only after public readiness and independent receiver setup.
func (r *ownerReadRelay) arm(uid string, delay time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.needle = binary.AppendUvarint(nil, uint64(len(uid)))
	r.needle = append(r.needle, uid...)
	r.delay = delay
	r.observation.ConfiguredDelayNanos = int64(delay)
}

func (r *ownerReadRelay) beginStopping() { r.mu.Lock(); r.stopping = true; r.mu.Unlock() }
func (r *ownerReadRelay) snapshot() relayObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.observation
}
func (r *ownerReadRelay) waitDelay(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return nil
	}
}

func (r *ownerReadRelay) failure(ctx context.Context, kind string) error {
	r.mu.Lock()
	if !r.stopping && ctx.Err() == nil && r.observation.FailureKind == "" {
		r.observation.FailureKind = kind
	}
	r.mu.Unlock()
	return errors.New("relay " + kind)
}

// match claims only one original remote owner-read, without changing its header.
func (r *ownerReadRelay) match(from, to uint64, header wire.Header, body []byte) (bool, time.Duration) {
	if len(body) < 6 || !bytes.Equal(body[:5], []byte{'W', 'K', 'V', 'P', 2}) {
		return false, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.needle) == 0 || !bytes.Contains(body[6:], r.needle) {
		return false, 0
	}
	if header.ServiceID == 13 && body[5] == 1 {
		r.observation.RegisterRequests++
	}
	if header.ServiceID != 14 || body[5] != 11 {
		return false, 0
	}
	r.observation.OwnerReadRequests++
	if r.delay <= 0 || r.observation.DelaysStarted != 0 || from == to {
		return false, 0
	}
	r.observation.DelaysStarted++
	r.observation.FromNode, r.observation.ToNode = from, to
	r.observation.BudgetMillis = header.BudgetMillis
	r.observation.BudgetPresent = header.Kind == transport.FrameKindRPCBudgetRequest
	return true, r.delay
}

// copy consumes one bounded header at a time; unrelated payloads remain streaming.
func (r *ownerReadRelay) copy(ctx context.Context, from, to uint64, dst io.Writer, src io.Reader) error {
	r.mu.Lock()
	r.observation.ActiveCopies++
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.observation.ActiveCopies--; r.mu.Unlock() }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var raw [wire.HeaderSize]byte
		if _, err := io.ReadFull(src, raw[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return r.failure(ctx, "header_read")
		}
		header, err := wire.DecodeHeader(raw[:], relayMaxBody)
		if err != nil {
			return r.failure(ctx, "header_invalid")
		}
		request := header.Kind == transport.FrameKindRPCRequest || header.Kind == transport.FrameKindRPCBudgetRequest
		inspect := request && (header.ServiceID == 13 || header.ServiceID == 14)
		var body []byte
		delayed := false
		if inspect {
			if header.BodyLen > relayPresenceBody {
				return r.failure(ctx, "presence_body_bound")
			}
			body = make([]byte, int(header.BodyLen))
			if _, err := io.ReadFull(src, body); err != nil {
				return r.failure(ctx, "presence_read")
			}
			var delay time.Duration
			delayed, delay = r.match(from, to, header, body)
			if delayed {
				began := time.Now()
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					r.mu.Lock()
					r.observation.HoldNanos = int64(time.Since(began))
					r.observation.DelaysCanceled++
					r.mu.Unlock()
					r.doneOnce.Do(func() { close(r.done) })
					return ctx.Err()
				case <-timer.C:
				}
				timer.Stop()
				r.mu.Lock()
				r.observation.HoldNanos = int64(time.Since(began))
				r.mu.Unlock()
			}
		}
		if err := writeRelayBytes(dst, raw[:]); err != nil {
			if delayed {
				r.doneOnce.Do(func() { close(r.done) })
			}
			return r.failure(ctx, "header_write")
		}
		if inspect {
			err = writeRelayBytes(dst, body)
		} else {
			_, err = io.CopyN(dst, src, int64(header.BodyLen))
		}
		if err != nil {
			if delayed {
				r.doneOnce.Do(func() { close(r.done) })
			}
			return r.failure(ctx, "body_forward")
		}
		r.mu.Lock()
		r.observation.FramesForwarded++
		if header.Kind == transport.FrameKindRPCCancel {
			r.observation.CancelFrames++
		}
		if delayed {
			r.observation.DelayedFramesForwarded++
		}
		r.mu.Unlock()
		if delayed {
			r.doneOnce.Do(func() { close(r.done) })
		}
	}
}

// writeRelayBytes preserves headers through writers that legally return partial writes.
func writeRelayBytes(dst io.Writer, body []byte) error {
	for len(body) > 0 {
		n, err := dst.Write(body)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(body) {
			return io.ErrShortWrite
		}
		body = body[n:]
	}
	return nil
}
