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
	parts := strings.Split(channelID, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", ErrInvalidPersonChannel
	}
	return parts[0], parts[1], nil
}

// NormalizePersonChannel returns the canonical person channel ID for sender input.
func NormalizePersonChannel(senderUID, channelID string) (string, error) {
	if senderUID == "" || channelID == "" {
		return "", ErrInvalidPersonChannel
	}
	if !strings.Contains(channelID, "@") {
		return EncodePersonChannel(senderUID, channelID), nil
	}
	left, right, err := DecodePersonChannel(channelID)
	if err != nil {
		return "", err
	}
	if left != senderUID && right != senderUID {
		return "", ErrInvalidPersonChannel
	}
	return EncodePersonChannel(left, right), nil
}
