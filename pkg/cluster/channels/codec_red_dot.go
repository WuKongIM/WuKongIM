package channels

import (
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

type messageWireField uint8

const (
	redDotWireField messageWireField = iota
	syncOnceWireField
	expireWireField
	publicationWireField
	invalidPublicationWireField
)

// payloadHasMessageFlag checks bounded RPC payloads. Legacy
// codecs must reject unsupported semantic fields instead of silently dropping them.
func payloadHasMessageFlag(payload any, field messageWireField) bool {
	switch v := payload.(type) {
	case ch.AppendRequest:
		return messageHasFlag(v.Message, field)
	case ch.AppendBatchRequest:
		return messagesHaveFlag(v.Messages, field)
	case ch.AppendResult:
		return messageHasFlag(v.Message, field)
	case ch.AppendBatchResult:
		for _, item := range v.Items {
			if messageHasFlag(item.Message, field) {
				return true
			}
		}
	case channeltransport.PullResponse:
		for _, record := range v.Records {
			if messageHasFlag(ch.Message{SyncOnce: record.SyncOnce, RedDot: record.RedDot, Expire: record.Expire, PublicationMetadata: record.PublicationMetadata}, field) {
				return true
			}
		}
	case channeltransport.PullBatchResponse:
		for _, item := range v.Items {
			if payloadHasMessageFlag(item.Response, field) {
				return true
			}
		}
	case LastVisibleResponse:
		return v.Found && messageHasFlag(v.Message, field)
	case ConversationHeadsResponse:
		for _, item := range v.Items {
			if item.Head.Found && messageHasFlag(item.Head.Message, field) {
				return true
			}
		}
	case CommittedReadsResponse:
		for _, item := range v.Items {
			if messagesHaveFlag(item.Read.Messages, field) {
				return true
			}
		}
	}
	return false
}

func messagesHaveFlag(messages []ch.Message, field messageWireField) bool {
	for _, msg := range messages {
		if messageHasFlag(msg, field) {
			return true
		}
	}
	return false
}

func messageHasFlag(msg ch.Message, field messageWireField) bool {
	switch field {
	case syncOnceWireField:
		return msg.SyncOnce
	case publicationWireField:
		return len(msg.PublicationMetadata) != 0
	case invalidPublicationWireField:
		if len(msg.PublicationMetadata) == 0 {
			return false
		}
		_, err := publication.Decode(msg.PublicationMetadata)
		return err != nil
	case expireWireField:
		return msg.Expire != 0
	default:
		return msg.RedDot
	}
}

func legacyMessageFlagError(payload any, version uint8) error {
	if version < codecVersion && payloadHasMessageFlag(payload, publicationWireField) {
		return errPublicationCodecRequired
	}
	if payloadHasMessageFlag(payload, invalidPublicationWireField) {
		return errPublicationMetadata
	}
	if version < legacyCodecVersionV8 && payloadHasMessageFlag(payload, redDotWireField) {
		return errRedDotCodecRequired
	}
	if version < legacyCodecVersionV8 && payloadHasMessageFlag(payload, syncOnceWireField) {
		return errSyncOnceCodecRequired
	}
	if version < legacyCodecVersionV9 && payloadHasMessageFlag(payload, expireWireField) {
		return errExpireCodecRequired
	}
	return nil
}
