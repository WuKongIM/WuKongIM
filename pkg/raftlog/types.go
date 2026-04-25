package raftlog

import (
	"encoding/binary"
	"errors"

	"go.etcd.io/raft/v3/raftpb"
)

const (
	logMetaHeaderSize   = 5*8 + 4
	groupMetaHeaderSize = logMetaHeaderSize
	appliedIndexSize    = 8
	defaultWriteChSize  = 1024
)

type logMeta struct {
	FirstIndex    uint64
	LastIndex     uint64
	AppliedIndex  uint64
	SnapshotIndex uint64
	SnapshotTerm  uint64
	ConfState     raftpb.ConfState
}

type groupMeta = logMeta

func (m logMeta) Marshal() ([]byte, error) {
	confState, err := m.ConfState.Marshal()
	if err != nil {
		return nil, err
	}

	data := make([]byte, 0, logMetaHeaderSize+len(confState))
	data = binary.BigEndian.AppendUint64(data, m.FirstIndex)
	data = binary.BigEndian.AppendUint64(data, m.LastIndex)
	data = binary.BigEndian.AppendUint64(data, m.AppliedIndex)
	data = binary.BigEndian.AppendUint64(data, m.SnapshotIndex)
	data = binary.BigEndian.AppendUint64(data, m.SnapshotTerm)
	data = binary.BigEndian.AppendUint32(data, uint32(len(confState)))
	data = append(data, confState...)
	return data, nil
}

func (m *logMeta) Unmarshal(data []byte) error {
	if len(data) < logMetaHeaderSize {
		return errors.New("raftstorage: invalid log metadata encoding")
	}

	m.FirstIndex = binary.BigEndian.Uint64(data[0:8])
	m.LastIndex = binary.BigEndian.Uint64(data[8:16])
	m.AppliedIndex = binary.BigEndian.Uint64(data[16:24])
	m.SnapshotIndex = binary.BigEndian.Uint64(data[24:32])
	m.SnapshotTerm = binary.BigEndian.Uint64(data[32:40])

	confStateSize := binary.BigEndian.Uint32(data[40:44])
	if len(data[44:]) != int(confStateSize) {
		return errors.New("raftstorage: invalid log metadata conf state size")
	}

	var confState raftpb.ConfState
	if confStateSize > 0 {
		if err := confState.Unmarshal(data[44:]); err != nil {
			return err
		}
	}
	m.ConfState = cloneConfState(confState)
	return nil
}
