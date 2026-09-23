package channelappend

import "github.com/WuKongIM/WuKongIM/pkg/protocol/publication"

// validSendPublication rejects malformed content before routing/allocation,
// including transient sends that will never reach durable storage validation.
func validSendPublication(value []byte) bool {
	if len(value) == 0 {
		return true
	}
	metadata, err := publication.Decode(value)
	if err != nil {
		return false
	}
	// Will acquires its real basis on append; one is a valid bounded placeholder.
	_, _, err = metadata.ExpiryDeadlineMS(1)
	return err == nil
}
