package channel

const (
	// DurableMessageCodecVersion and DurableMessageHeaderSize define the durable
	// message payload contract shared by log encoding and store-side apply-fetch
	// idempotency reconstruction.
	DurableMessageCodecVersion byte = 1
	DurableMessageHeaderSize        = 45
)
