package conn

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

const rpcTraceSampleBit uint64 = 1 << 63

// NegotiateRPCTrace binds one opaque identity to this connection generation.
// Only explicit absence is compatible fallback; failures never replay business RPC.
func (c *Conn) NegotiateRPCTrace(ctx context.Context) error {
	for {
		c.traceMu.Lock()
		if c.traceState != 0 {
			err := c.traceErr
			c.traceMu.Unlock()
			return err
		}
		if ready := c.traceReady; ready != nil {
			c.traceMu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return ctx.Err()
			case <-c.ctx.Done():
				return core.ErrStopped
			}
		}
		ready := make(chan struct{})
		c.traceReady = ready
		c.traceMu.Unlock()
		var token [16]byte
		_, err := rand.Read(token[:])
		var response []byte
		payload := append([]byte(wire.CapabilityRPCTrace), token[:]...)
		if err == nil {
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			response, err = c.Call(probeCtx, Outbound{ServiceID: wire.TraceCapabilityServiceID, Priority: core.PriorityControl, Payload: core.NewOwnedBuffer(payload, nil)})
			cancel()
		}
		state := uint8(3)
		if err == nil {
			if bytes.Equal(response, payload) {
				state = 2
			} else {
				err = fmt.Errorf("%w: invalid trace capability response", core.ErrInvalidFrame)
			}
		} else {
			var remote core.RemoteError
			if errors.As(err, &remote) && remote.Code == core.RemoteErrorCodeServiceNotFound {
				state = 1
				err = nil
			}
		}
		c.traceMu.Lock()
		c.traceState = state
		c.traceErr = err
		if state == 2 {
			c.traceToken = token
		}
		c.traceReady = nil
		close(ready)
		c.traceMu.Unlock()
		return err
	}
}

// AcceptRPCTrace binds a receiver connection once; repeated exact probes are safe.
func (c *Conn) AcceptRPCTrace(token [16]byte) bool {
	if token == [16]byte{} {
		return false
	}
	c.traceMu.Lock()
	defer c.traceMu.Unlock()
	if c.traceState != 0 {
		return c.traceState == 2 && c.traceToken == token
	}
	c.traceToken = token
	c.traceState = 2
	return true
}

// AcceptRPCTracePayload rejects malformed capability envelopes without touching business bytes.
func (c *Conn) AcceptRPCTracePayload(payload []byte) bool {
	if len(payload) != len(wire.CapabilityRPCTrace)+16 || !bytes.Equal(payload[:len(wire.CapabilityRPCTrace)], []byte(wire.CapabilityRPCTrace)) {
		return false
	}
	var token [16]byte
	copy(token[:], payload[len(wire.CapabilityRPCTrace):])
	return c.AcceptRPCTrace(token)
}

// TraceIdentity recognizes sampling only after this exact connection negotiated it.
func (c *Conn) TraceIdentity(requestID uint64) ([16]byte, bool) {
	if requestID&rpcTraceSampleBit == 0 {
		return [16]byte{}, false
	}
	c.traceMu.Lock()
	defer c.traceMu.Unlock()
	return c.traceToken, c.traceState == 2 && requestID&rpcTraceSampleBit != 0 && requestID&^rpcTraceSampleBit != 0
}

// responseObservation closes the caller wait even when response and write completion race.
// Its lock protects only one bounded span; it never waits for I/O or observer delivery.
type responseObservation struct {
	mu    sync.Mutex
	trace *rpctrace.Trace
	wait  rpctrace.Span
	done  bool
}

func (o *responseObservation) written(err error) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.done && err == nil {
		o.wait = o.trace.Begin(rpctrace.StageResponseWait)
	}
}
func (o *responseObservation) finish(result rpctrace.Result) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.done = true
	o.wait.End(result)
}

// finishTraceWrite releases response observation ownership on every terminal queue/write path.
func (o Outbound) finishTraceWrite(err error) {
	o.traceQueue.End(core.RPCTraceResult(err))
	o.traceWrite.End(core.RPCTraceResult(err))
	o.responseObservation.written(err)
	if o.FinishTrace {
		result := o.TraceResult
		if err != nil {
			result = core.RPCTraceResult(err)
		}
		core.FinishRPCTrace(o.traceObserver, o.traceNodeID, o.Trace, result)
	}
}
