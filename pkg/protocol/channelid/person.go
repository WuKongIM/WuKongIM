package channelid

import (
	"errors"
	"hash/crc32"
	"strings"
)

// ErrInvalidPersonChannel reports malformed person channel input.
var ErrInvalidPersonChannel = errors.New("runtime/channelid: invalid person channel")

// EncodePersonChannel returns the deterministic channel ID for two person UIDs.
func EncodePersonChannel(leftUID, rightUID string) string {
	leftHash := crc32.ChecksumIEEE([]byte(leftUID))
	rightHash := crc32.ChecksumIEEE([]byte(rightUID))
	if leftHash > rightHash {
		return leftUID + "@" + rightUID
	}
	if leftHash == rightHash && leftUID > rightUID {
		return leftUID + "@" + rightUID
	}
	return rightUID + "@" + leftUID
}

// DecodePersonChannel splits a person channel ID into its two UID parts.
func DecodePersonChannel(channelID string) (string, string, error) {
	left, right, pair := strings.Cut(channelID, "@")
	if !pair || left == "" || right == "" || strings.Contains(right, "@") {
		return "", "", ErrInvalidPersonChannel
	}
	return left, right, nil
}

// NormalizePersonChannel returns the canonical person channel ID for sender input.
func NormalizePersonChannel(senderUID, channelID string) (string, error) {
	peer, err := PersonChannelPeer(senderUID, channelID)
	if err != nil {
		return "", err
	}
	return EncodePersonChannel(senderUID, peer), nil
}

// PersonChannelPeer resolves the other UID without constructing a Channel ID.
// A peer alone identifies a Person Channel only within one sender's scope.
func PersonChannelPeer(senderUID, channelID string) (string, error) {
	if senderUID == "" || channelID == "" {
		return "", ErrInvalidPersonChannel
	}
	if !strings.Contains(channelID, "@") {
		return channelID, nil
	}
	left, right, err := DecodePersonChannel(channelID)
	if err != nil {
		return "", err
	}
	if left == senderUID {
		return right, nil
	}
	if right == senderUID {
		return left, nil
	}
	return "", ErrInvalidPersonChannel
}
