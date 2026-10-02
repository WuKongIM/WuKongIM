package channelappend

import "github.com/WuKongIM/WuKongIM/pkg/protocol/publication"

// validSendPublication rejects malformed content before routing/allocation,
// including transient sends that will never reach durable storage validation.
func validSendPublication(value []byte) bool {
	return publication.ValidateSend(value) == nil
}
