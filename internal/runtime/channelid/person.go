package channelid

import (
	"errors"
	"hash/crc32"
	"strings"
)

var ErrInvalidPersonChannel = errors.New("runtime/channelid: invalid person channel")

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

func DecodePersonChannel(channelID string) (string, string, error) {
	parts := strings.Split(channelID, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", ErrInvalidPersonChannel
	}
	return parts[0], parts[1], nil
}

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
