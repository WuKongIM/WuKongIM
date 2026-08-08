package message

import (
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

const maxAppendKeyStringLen = 1<<16 - 1

// appendKeyCache stores immutable key prefixes used by append and append validation.
type appendKeyCache struct {
	// rowPrefix is the primary message row prefix before sequence and family suffixes.
	rowPrefix []byte
	// globalMessageIDIndexPrefix is the canonical message ID index prefix.
	globalMessageIDIndexPrefix []byte
	// clientMsgNoIndexPrefix is the client message number index prefix before client key parts.
	clientMsgNoIndexPrefix []byte
	// idempotencyIndexPrefix is the sender/client-message index prefix before sender key parts.
	idempotencyIndexPrefix []byte
	// senderSeqIndexPrefix is the sender/sequence index prefix before sender key parts.
	senderSeqIndexPrefix []byte
	// catalogKey is the immutable catalog key for this channel.
	catalogKey []byte
	// catalogValue is the immutable catalog value for this channel identity.
	catalogValue []byte
}

func newAppendKeyCache(key ChannelKey, id ChannelID) appendKeyCache {
	return appendKeyCache{
		rowPrefix:                  encodeMessageRowPrefix(key),
		globalMessageIDIndexPrefix: encodeGlobalMessageIDIndexPrefix(),
		clientMsgNoIndexPrefix:     encodeMessageIndexPrefix(key, messageIndexIDClientMsgNo),
		idempotencyIndexPrefix:     encodeMessageIndexPrefix(key, messageIndexIDFromUIDClientMsgNo),
		senderSeqIndexPrefix:       encodeMessageIndexPrefix(key, messageIndexIDFromUIDMessageSeq),
		catalogKey:                 encodeCatalogKey(key),
		catalogValue:               encodeCatalogValue(id),
	}
}

func (c appendKeyCache) initialized() bool {
	return len(c.rowPrefix) > 0
}

func (c appendKeyCache) messageRowKey(seq uint64, familyID uint16) []byte {
	key := make([]byte, c.messageRowKeyLen())
	c.writeMessageRowKey(key, seq, familyID)
	return key
}

func (c appendKeyCache) messageRowKeyLen() int {
	return len(c.rowPrefix) + 10
}

func (c appendKeyCache) writeMessageRowKey(dst []byte, seq uint64, familyID uint16) {
	copy(dst, c.rowPrefix)
	offset := len(c.rowPrefix)
	binary.BigEndian.PutUint64(dst[offset:offset+8], seq)
	binary.BigEndian.PutUint16(dst[offset+8:offset+10], familyID)
}

func (c appendKeyCache) globalMessageIDIndexKeyTo(dst []byte, messageID uint64) []byte {
	keyLen := len(c.globalMessageIDIndexPrefix) + 8
	if cap(dst) < keyLen {
		dst = make([]byte, keyLen)
	} else {
		dst = dst[:keyLen]
	}
	copy(dst, c.globalMessageIDIndexPrefix)
	binary.BigEndian.PutUint64(dst[len(c.globalMessageIDIndexPrefix):], messageID)
	return dst
}

func (c appendKeyCache) clientMsgNoIndexKey(clientMsgNo string, seq uint64) []byte {
	key := make([]byte, 0, len(c.clientMsgNoIndexPrefix)+2+len(clientMsgNo)+8)
	key = append(key, c.clientMsgNoIndexPrefix...)
	key = keycodec.AppendString(key, clientMsgNo)
	return keycodec.AppendUint64(key, seq)
}

func (c appendKeyCache) clientMsgNoIndexKeyLen(clientMsgNo string) int {
	validateAppendKeyStringLen(clientMsgNo)
	return len(c.clientMsgNoIndexPrefix) + 2 + len(clientMsgNo) + 8
}

func (c appendKeyCache) writeClientMsgNoIndexKey(dst []byte, clientMsgNo string, seq uint64) {
	copy(dst, c.clientMsgNoIndexPrefix)
	offset := len(c.clientMsgNoIndexPrefix)
	offset = writeAppendKeyString(dst, offset, clientMsgNo)
	binary.BigEndian.PutUint64(dst[offset:], seq)
}

func (c appendKeyCache) idempotencyIndexKey(fromUID string, clientMsgNo string) []byte {
	key := make([]byte, 0, len(c.idempotencyIndexPrefix)+4+len(fromUID)+len(clientMsgNo))
	key = append(key, c.idempotencyIndexPrefix...)
	key = keycodec.AppendString(key, clientMsgNo)
	return keycodec.AppendString(key, fromUID)
}

func (c appendKeyCache) idempotencyIndexKeyLen(fromUID string, clientMsgNo string) int {
	validateAppendKeyStringLen(fromUID)
	validateAppendKeyStringLen(clientMsgNo)
	return len(c.idempotencyIndexPrefix) + 4 + len(fromUID) + len(clientMsgNo)
}

func (c appendKeyCache) idempotencyIndexKeyTo(dst []byte, fromUID string, clientMsgNo string) []byte {
	keyLen := c.idempotencyIndexKeyLen(fromUID, clientMsgNo)
	if cap(dst) < keyLen {
		dst = make([]byte, keyLen)
	} else {
		dst = dst[:keyLen]
	}
	c.writeIdempotencyIndexKey(dst, fromUID, clientMsgNo)
	return dst
}

func (c appendKeyCache) writeIdempotencyIndexKey(dst []byte, fromUID string, clientMsgNo string) {
	copy(dst, c.idempotencyIndexPrefix)
	offset := len(c.idempotencyIndexPrefix)
	offset = writeAppendKeyString(dst, offset, clientMsgNo)
	writeAppendKeyString(dst, offset, fromUID)
}

func (c appendKeyCache) senderSeqIndexKeyLen(fromUID string) int {
	validateAppendKeyStringLen(fromUID)
	return len(c.senderSeqIndexPrefix) + 2 + len(fromUID) + 8
}

func (c appendKeyCache) writeSenderSeqIndexKey(dst []byte, fromUID string, seq uint64) {
	copy(dst, c.senderSeqIndexPrefix)
	offset := writeAppendKeyString(dst, len(c.senderSeqIndexPrefix), fromUID)
	binary.BigEndian.PutUint64(dst[offset:], seq)
}

func writeAppendKeyString(dst []byte, offset int, value string) int {
	validateAppendKeyStringLen(value)
	binary.BigEndian.PutUint16(dst[offset:offset+2], uint16(len(value)))
	copy(dst[offset+2:], value)
	return offset + 2 + len(value)
}

func validateAppendKeyStringLen(value string) {
	if len(value) > maxAppendKeyStringLen {
		panic("message: string key part too long")
	}
}
