package channel

import channelcompat "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"

const (
	// DurableMessageCodecVersion and DurableMessageHeaderSize define the durable
	// message payload contract shared by log encoding and store-side apply-fetch
	// idempotency reconstruction.
	DurableMessageCodecVersion = channelcompat.DurableMessageCodecVersion
	DurableMessageHeaderSize   = channelcompat.DurableMessageHeaderSize
)
