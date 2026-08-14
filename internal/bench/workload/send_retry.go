package workload

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const fixedSendRetryCount = model.TrafficRetryMaximumRetries

type sendRetryOptions struct {
	enabled     bool
	operation   string
	fallbackSeq uint64
	client      PersonClient
	packet      *frame.SendPacket
	metrics     *metrics.Registry
	labels      metrics.Labels
	sleep       func(context.Context, time.Duration) error
	withTimeout func(context.Context) (context.Context, context.CancelFunc)
}

type sendAttemptError struct{ err error }

func (e *sendAttemptError) Error() string { return e.err.Error() }
func (e *sendAttemptError) Unwrap() error { return e.err }

// sendPacketWithRetry owns one logical SEND from first admission through its
// terminal SENDACK. A retry changes only ClientSeq; ClientMsgNo is immutable.
func sendPacketWithRetry(ctx context.Context, opts sendRetryOptions) (result *frame.SendackPacket, resultErr error) {
	if !opts.enabled {
		pkt := *opts.packet
		pkt.ClientSeq = nextSendClientSeq(opts.client, opts.fallbackSeq)
		if err := opts.client.Send(ctx, &pkt); err != nil {
			return nil, &sendAttemptError{err: err}
		}
		return waitForExactSendack(ctx, opts, map[uint64]struct{}{pkt.ClientSeq: {}}, true)
	}
	if opts.metrics == nil || opts.packet == nil || opts.client == nil || opts.sleep == nil || opts.withTimeout == nil {
		return nil, fmt.Errorf("%s workload: retry evidence dependencies are incomplete", opts.operation)
	}

	opts.metrics.IncCounter("logical_identity_total", opts.labels)
	opts.metrics.IncCounter("logical_sent_total", opts.labels)
	opts.metrics.AddGauge("logical_remaining", opts.labels, 1)
	opts.metrics.SetGauge("configured_maximum_attempts", opts.labels, fixedSendRetryCount+1)
	defer func() {
		if resultErr != nil && ctx.Err() != nil && errors.Is(resultErr, ctx.Err()) {
			opts.metrics.IncCounter("logical_shutdown_cancellation_total", opts.labels)
		} else if resultErr != nil {
			opts.metrics.IncCounter("logical_terminal_error_total", opts.labels)
		}
		opts.metrics.AddGauge("logical_remaining", opts.labels, -1)
	}()

	pendingSeqs := make(map[uint64]struct{}, fixedSendRetryCount+1)
	for attempt := 0; attempt <= fixedSendRetryCount; attempt++ {
		if attempt > 0 {
			if err := opts.sleep(ctx, fixedSendRetryDelay(attempt)); err != nil {
				return nil, err
			}
			opts.metrics.IncCounter("retry_attempt_total", opts.labels)
		}

		pkt := *opts.packet
		pkt.ClientSeq = nextSendClientSeq(opts.client, opts.fallbackSeq+uint64(attempt))
		opts.metrics.IncCounter("send_attempt_total", opts.labels)
		opts.metrics.IncCounter("attempt_record_total", opts.labels)
		opts.metrics.SetGaugeMax("maximum_observed_attempts", opts.labels, float64(attempt+1))
		if err := opts.client.Send(ctx, &pkt); err != nil {
			if ctx.Err() != nil || attempt == fixedSendRetryCount {
				if ctx.Err() == nil {
					opts.metrics.IncCounter("retry_exhausted_total", opts.labels)
				}
				return nil, &sendAttemptError{err: err}
			}
			continue
		}
		pendingSeqs[pkt.ClientSeq] = struct{}{}

		for {
			ack, err := waitForExactSendack(ctx, opts, pendingSeqs, false)
			if err != nil {
				if ctx.Err() != nil || attempt == fixedSendRetryCount {
					if ctx.Err() == nil {
						opts.metrics.IncCounter("retry_exhausted_total", opts.labels)
					}
					return nil, err
				}
				break
			}
			if ack.ClientMsgNo != opts.packet.ClientMsgNo {
				opts.metrics.IncCounter("client_msg_no_mismatch_total", opts.labels)
				opts.metrics.IncCounter("logical_correctness_error_total", opts.labels)
				return nil, fmt.Errorf("%s workload: SENDACK changed client_msg_no", opts.operation)
			}
			delete(pendingSeqs, ack.ClientSeq)
			switch {
			case ack.ReasonCode == frame.ReasonSuccess:
				opts.metrics.IncCounter("sendack_success_total", opts.labels)
				return ack, nil
			case !retriableGenericSendackReason(ack.ReasonCode):
				opts.metrics.IncCounter("logical_correctness_error_total", opts.labels)
				return nil, fmt.Errorf("%s workload: sendack rejected message with reason %s", opts.operation, ack.ReasonCode)
			case ack.ClientSeq != pkt.ClientSeq:
				// A delayed rejection from an older attempt does not consume another
				// retry or displace the current attempt's wait.
				continue
			case attempt == fixedSendRetryCount:
				opts.metrics.IncCounter("retry_exhausted_total", opts.labels)
				return nil, fmt.Errorf("%s workload: sendack retry exhausted with reason %s", opts.operation, ack.ReasonCode)
			default:
				break
			}
			break
		}
	}
	return nil, fmt.Errorf("%s workload: retry state exhausted", opts.operation)
}

func fixedSendRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return model.TrafficRetryFirstDelay
	case 2:
		return model.TrafficRetrySecondDelay
	case 3:
		return model.TrafficRetryThirdDelay
	default:
		return 0
	}
}

func sendFailureOperation(prefix string, err error) string {
	var sendErr *sendAttemptError
	if errors.As(err, &sendErr) {
		return prefix + " send"
	}
	return prefix + " sendack"
}

func waitForExactSendack(ctx context.Context, opts sendRetryOptions, expectedSeqs map[uint64]struct{}, requireClientMsgNo bool) (*frame.SendackPacket, error) {
	deadlineCtx, cancel := opts.withTimeout(ctx)
	defer cancel()
	f, err := readFrameMatching(deadlineCtx, opts.client, func(f frame.Frame) bool {
		ack, ok := f.(*frame.SendackPacket)
		if !ok {
			return false
		}
		_, ok = expectedSeqs[ack.ClientSeq]
		return ok && (!requireClientMsgNo || ack.ClientMsgNo == opts.packet.ClientMsgNo)
	})
	if err != nil {
		return nil, fmt.Errorf("%s workload: sendack not received: %w", opts.operation, err)
	}
	ack, ok := f.(*frame.SendackPacket)
	if !ok {
		return nil, errors.New("send retry: matched frame is not SENDACK")
	}
	return ack, nil
}

func retriableGenericSendackReason(reason frame.ReasonCode) bool {
	switch reason {
	case frame.ReasonUnknown, frame.ReasonUserNotOnNode, frame.ReasonForwardSendPacketError,
		frame.ReasonSystemError, frame.ReasonNodeMatchError, frame.ReasonNodeNotMatch,
		frame.ReasonRateLimit:
		return true
	default:
		return false
	}
}
