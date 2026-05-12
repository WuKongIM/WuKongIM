package channelid

import (
	"errors"
	"strings"
)

// ErrInvalidAgentChannel indicates an agent channel ID is not in uid@agentUID form.
var ErrInvalidAgentChannel = errors.New("runtime/channelid: invalid agent channel")

// EncodeAgentChannel returns the legacy agent channel ID for a user and an agent.
func EncodeAgentChannel(uid, agentUID string) string {
	return uid + "@" + agentUID
}

// DecodeAgentChannel splits a legacy agent channel ID into user and agent UIDs.
func DecodeAgentChannel(channelID string) (string, string, error) {
	parts := strings.Split(channelID, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", ErrInvalidAgentChannel
	}
	return parts[0], parts[1], nil
}
