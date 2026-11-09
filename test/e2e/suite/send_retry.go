package suite

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// Retryable SENDACK policy. The product contract (messaging guide and
// internal/usecase/message ReasonNodeNotMatch) asks clients to retry these
// reasons with the same client_msg_no through a fresher route, for example
// across planned Slot replica moves.
const (
	sendRetryMaxAttempts = 10
	sendRetryBackoff     = 200 * time.Millisecond
)

// RetryableSendackReason reports whether a SENDACK reason is transient and may
// be retried with the same client_msg_no. Rejections are never retryable.
func RetryableSendackReason(reason frame.ReasonCode) bool {
	switch reason {
	case frame.ReasonNodeNotMatch, frame.ReasonNodeMatchError, frame.ReasonSystemError,
		frame.ReasonSystemBusy, frame.ReasonRateLimit, frame.ReasonForwardSendPacketError:
		return true
	default:
		return false
	}
}

// SendWithRetry sends packet and retries retryable SENDACK reasons with the
// same ClientSeq and ClientMsgNo, at most 10 attempts with a 200ms backoff. It
// returns the final SENDACK and the number of retries. A non-success final
// SENDACK is returned with an error so callers can still inspect it.
func (c *WKProtoClient) SendWithRetry(packet *frame.SendPacket) (*frame.SendackPacket, int, error) {
	var ack *frame.SendackPacket
	for attempt := 1; ; attempt++ {
		if err := c.SendFrame(packet); err != nil {
			return nil, attempt - 1, err
		}
		var err error
		ack, err = c.ReadSendAck()
		if err != nil {
			return nil, attempt - 1, err
		}
		if ack.ReasonCode == frame.ReasonSuccess {
			return ack, attempt - 1, nil
		}
		if !RetryableSendackReason(ack.ReasonCode) || attempt >= sendRetryMaxAttempts {
			return ack, attempt - 1, fmt.Errorf("sendack reason=%v after %d attempts msg_no=%s", ack.ReasonCode, attempt, packet.ClientMsgNo)
		}
		time.Sleep(sendRetryBackoff)
	}
}
