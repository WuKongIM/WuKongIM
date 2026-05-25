package raftstore

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestRecordRoundTripEntries(t *testing.T) {
	entries := []raftpb.Entry{{Index: 1, Term: 1, Type: raftpb.EntryNormal, Data: []byte("one")}}
	payload, err := marshalEntryRecord(entries)
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, writeRecord(&buf, walRecord{Type: recordEntries, Payload: payload}, 0))

	got, crc, err := readRecord(&buf, 0)
	require.NoError(t, err)
	require.NotZero(t, crc)
	require.Equal(t, recordEntries, got.Type)
	decoded, err := unmarshalEntryRecord(got.Payload)
	require.NoError(t, err)
	require.Equal(t, entries, decoded)
}

func TestReadRecordRejectsCorruptCRC(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeRecord(&buf, walRecord{Type: recordAppliedIndex, Payload: marshalUint64(9)}, 0))
	data := append([]byte(nil), buf.Bytes()...)
	data[len(data)-1] ^= 0xff
	_, _, err := readRecord(bytes.NewReader(data), 0)
	require.ErrorIs(t, err, ErrCRCMismatch)
}
