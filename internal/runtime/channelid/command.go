package channelid

import "strings"

// CommandChannelSuffix is the legacy suffix used to address command channels.
const CommandChannelSuffix = "____cmd"

// IsCommandChannel reports whether channelID uses the legacy command suffix.
func IsCommandChannel(channelID string) bool {
	return strings.HasSuffix(channelID, CommandChannelSuffix)
}

// ToCommandChannel returns channelID with the legacy command suffix applied once.
func ToCommandChannel(channelID string) string {
	if IsCommandChannel(channelID) {
		return channelID
	}
	return channelID + CommandChannelSuffix
}

// FromCommandChannel removes the legacy command suffix when present.
func FromCommandChannel(channelID string) (string, bool) {
	if !IsCommandChannel(channelID) {
		return channelID, false
	}
	return strings.TrimSuffix(channelID, CommandChannelSuffix), true
}
