package raftlog

import "encoding/binary"

const (
	currentFormatVersion byte = 0x01
	globalScopeKind      byte = 0x00

	recordTypeManifest     byte = 0x01
	recordTypeHardState    byte = 0x11
	recordTypeAppliedIndex byte = 0x12
	recordTypeSnapshot     byte = 0x13
	recordTypeMeta         byte = 0x14
	recordTypeEntry        byte = 0x20
)

func encodeManifestKey() []byte {
	return []byte{currentFormatVersion, globalScopeKind, recordTypeManifest}
}

func encodeScopePrefix(scope Scope) []byte {
	key := make([]byte, 0, 1+1+8)
	key = append(key, currentFormatVersion, byte(scope.Kind))
	key = binary.BigEndian.AppendUint64(key, scope.ID)
	return key
}

func encodeMetaKey(scope Scope, recordType byte) []byte {
	key := make([]byte, 0, 1+1+8+1)
	key = append(key, encodeScopePrefix(scope)...)
	key = append(key, recordType)
	return key
}

func encodeHardStateKey(scope Scope) []byte {
	return encodeMetaKey(scope, recordTypeHardState)
}

func encodeAppliedIndexKey(scope Scope) []byte {
	return encodeMetaKey(scope, recordTypeAppliedIndex)
}

func encodeSnapshotKey(scope Scope) []byte {
	return encodeMetaKey(scope, recordTypeSnapshot)
}

func encodeGroupStateKey(scope Scope) []byte {
	return encodeMetaKey(scope, recordTypeMeta)
}

func encodeEntryPrefix(scope Scope) []byte {
	return encodeMetaKey(scope, recordTypeEntry)
}

func encodeEntryPrefixEnd(scope Scope) []byte {
	return nextPrefix(encodeEntryPrefix(scope))
}

func encodeEntryKey(scope Scope, index uint64) []byte {
	key := make([]byte, 0, 1+1+8+1+8)
	key = append(key, encodeEntryPrefix(scope)...)
	key = binary.BigEndian.AppendUint64(key, index)
	return key
}

func nextPrefix(prefix []byte) []byte {
	end := append([]byte(nil), prefix...)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] == 0xff {
			continue
		}
		end[i]++
		return end[:i+1]
	}
	return []byte{0xff}
}
