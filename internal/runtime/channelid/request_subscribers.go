package channelid

import (
	"errors"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// ErrRequestSubscribersRequired indicates that request-scoped delivery has no usable subscriber UIDs.
var ErrRequestSubscribersRequired = errors.New("runtime/channelid: request subscribers required")

// RequestSubscriberChannel describes the internal temp channel derived for a request-scoped send.
type RequestSubscriberChannel struct {
	// SourceChannelID is the stable temporary channel ID derived from the subscriber snapshot.
	SourceChannelID string
	// CommandChannelID is SourceChannelID with the legacy command suffix applied.
	CommandChannelID string
	// ChannelType is always the temporary channel type for request-scoped sends.
	ChannelType uint8
	// Subscribers is the normalized first-seen subscriber snapshot.
	Subscribers []string
}

// NormalizeRequestSubscribers trims empty values and removes duplicate UIDs while preserving first-seen order.
func NormalizeRequestSubscribers(subscribers []string) []string {
	if len(subscribers) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(subscribers))
	out := make([]string, 0, len(subscribers))
	for _, subscriber := range subscribers {
		uid := strings.TrimSpace(subscriber)
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		out = append(out, uid)
	}
	return out
}

// RequestSubscriberChannelFor derives the stable temp and command channel IDs for a subscriber snapshot.
func RequestSubscriberChannelFor(subscribers []string) (RequestSubscriberChannel, error) {
	normalized := NormalizeRequestSubscribers(subscribers)
	if len(normalized) == 0 {
		return RequestSubscriberChannel{}, ErrRequestSubscribersRequired
	}

	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.Join(normalized, ",")))
	sourceID := strconv.FormatUint(h.Sum64(), 10)
	return RequestSubscriberChannel{
		SourceChannelID:  sourceID,
		CommandChannelID: ToCommandChannel(sourceID),
		ChannelType:      frame.ChannelTypeTemp,
		Subscribers:      normalized,
	}, nil
}
