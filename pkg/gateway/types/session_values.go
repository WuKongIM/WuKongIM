package types

const (
	// SessionValuePeerAddr is the physical transport peer (host:port), retained for
	// diagnostics independently of the effective client address in RemoteAddr.
	SessionValuePeerAddr          = "gateway.peer_addr"
	SessionValueUID               = "gateway.uid"
	SessionValueDeviceID          = "gateway.device_id"
	SessionValueDeviceFlag        = "gateway.device_flag"
	SessionValueDeviceLevel       = "gateway.device_level"
	SessionValueProtocolVersion   = "gateway.protocol_version"
	SessionValueProtocolName      = "gateway.protocol_name"
	SessionValueEncryptionEnabled = "gateway.encryption_enabled"
	SessionValueAESKey            = "gateway.aes_key"
	SessionValueAESIV             = "gateway.aes_iv"
	SessionValueCrypto            = "gateway.wkproto_crypto"
)
