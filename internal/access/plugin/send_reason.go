package plugin

import (
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// mapSendReason uses the same wire reasons as HTTP and WKProto. The legacy
// SendResp has no reason field, so rejected sends use an RPC error body.
func mapSendReason(reason message.Reason) frame.ReasonCode {
	// Business rejection codes occupy the same reserved range on every entry.
	if message.IsBusinessReasonCode(uint32(reason)) {
		return frame.ReasonCode(reason)
	}
	switch reason {
	case message.ReasonSuccess:
		return frame.ReasonSuccess
	case message.ReasonAuthFail:
		return frame.ReasonAuthFail
	case message.ReasonChannelNotExist:
		return frame.ReasonChannelNotExist
	case message.ReasonNodeNotMatch:
		return frame.ReasonNodeNotMatch
	case message.ReasonSubscriberNotExist:
		return frame.ReasonSubscriberNotExist
	case message.ReasonInBlacklist:
		return frame.ReasonInBlacklist
	case message.ReasonNotAllowSend:
		return frame.ReasonNotAllowSend
	case message.ReasonNotInWhitelist:
		return frame.ReasonNotInWhitelist
	case message.ReasonBan:
		return frame.ReasonBan
	case message.ReasonDisband:
		return frame.ReasonDisband
	case message.ReasonSendBan:
		return frame.ReasonSendBan
	case message.ReasonInvalidRequest, message.ReasonUnsupported:
		return frame.ReasonPayloadDecodeError
	default:
		return frame.ReasonSystemError
	}
}
