// Package protocolmeta defines protocol value contracts used by entry-independent usecases.
package protocolmeta

// DeviceFlag identifies a client device category without importing frame implementations.
type DeviceFlag uint8

const (
	// DeviceFlagApp identifies native application clients.
	DeviceFlagApp DeviceFlag = iota
	// DeviceFlagWeb identifies web clients.
	DeviceFlagWeb
	// DeviceFlagPC identifies desktop clients.
	DeviceFlagPC
	// DeviceFlagSystem identifies internal system clients.
	DeviceFlagSystem DeviceFlag = 99
)

// DeviceLevel identifies the conflict policy for a device category.
type DeviceLevel uint8

const (
	// DeviceLevelSlave allows multiple devices of the same category.
	DeviceLevelSlave DeviceLevel = iota
	// DeviceLevelMaster allows one active device of the same category.
	DeviceLevelMaster
)

// ChannelType identifies the channel semantics needed by entry-independent usecases.
type ChannelType uint8

const (
	// ChannelTypePerson identifies a person-to-person channel.
	ChannelTypePerson ChannelType = 1
	// ChannelTypeTemp identifies a request-scoped temporary channel.
	ChannelTypeTemp ChannelType = 8
	// ChannelTypeSystemUIDRegistry identifies the reserved system UID storage channel.
	ChannelTypeSystemUIDRegistry ChannelType = 99
)
