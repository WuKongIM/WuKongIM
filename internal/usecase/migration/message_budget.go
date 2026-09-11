package migration

import (
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// PrepareMessageRecord maps original seconds to the existing server timestamp
// and rejects fields the target v3 read/replication path cannot preserve.
// It never changes IDs, sequences, payloads or storage/proposal formats.
func PrepareMessageRecord(m channelcompat.Message) (channelcompat.Record, quorumlog.Record, error) {
	row, record, err := encodeRecoveryMessage(m)
	if err != nil {
		return row, record, err
	}
	unsupported := UnsupportedMessageFields(m)
	if len(unsupported) != 0 {
		return channelcompat.Record{}, quorumlog.Record{}, fmt.Errorf("source message %d is incompatible with existing v3 message reads and recovery: %s; message retained in source, no storage exception or automatic exclusion", m.MessageSeq, strings.Join(unsupported, ", "))
	}
	// Existing replica reconstruction uses ServerTimestampMS and leaves the
	// redundant legacy timestamp column zero. Use that same canonical mapping.
	m.Timestamp = 0
	row, err = message.EncodeMessageRecord(m, 1)
	return row, record, err
}

// UnsupportedMessageFields lists every protocol field that existing v3 reads
// and replica recovery cannot preserve. Values and private payloads are omitted.
func UnsupportedMessageFields(m channelcompat.Message) []string {
	var unsupported []string
	add := func(present bool, field string) {
		if present {
			unsupported = append(unsupported, field)
		}
	}
	add(m.Framer.NoPersist, "no_persist")
	// Existing node RPC does not carry SyncOnce. A node-local read alone is
	// insufficient evidence for changed-topology migration and replica repair.
	add(m.Framer.SyncOnce, "sync_once")
	add(m.Framer.DUP, "dup")
	add(m.Framer.HasServerVersion, "has_server_version")
	add(m.Framer.End, "end")
	add(m.ClientSeq != 0, "client_seq")
	add(m.MsgKey != "", "msg_key")
	add(m.StreamNo != "", "stream_no")
	add(m.StreamID != 0, "stream_id")
	add(m.StreamFlag != 0, "stream_flag")
	add(m.Topic != "", "topic")
	add(m.Timestamp <= 0 || m.ServerTimestampMS != int64(m.Timestamp)*1000, "timestamp")
	return unsupported
}

// encodeRecoveryMessage checks the existing record encoding and byte bounds
// independently of optional field compatibility, so diagnostics can report both.
func encodeRecoveryMessage(m channelcompat.Message) (channelcompat.Record, quorumlog.Record, error) {
	row, err := message.EncodeMessageRecord(m, 1)
	if err != nil {
		return row, quorumlog.Record{}, err
	}
	record := quorumlog.Record{ID: m.MessageID, Index: m.MessageSeq, Epoch: 1, FromUID: m.FromUID, ClientMsgNo: m.ClientMsgNo, ServerTimestampMS: m.ServerTimestampMS, Setting: uint8(m.Setting), SyncOnce: m.Framer.SyncOnce, Payload: m.Payload, Expire: m.Expire}
	if len(row.Payload) > quorumlog.DefaultRecoveryPageBytes || quorumlog.RecoveryRecordBytes(record.FromUID, record.ClientMsgNo, len(record.Payload)) > quorumlog.DefaultRecoveryPageBytes {
		return channelcompat.Record{}, quorumlog.Record{}, fmt.Errorf("source message %d exceeds the native recovery page budget of %d bytes", m.MessageSeq, quorumlog.DefaultRecoveryPageBytes)
	}
	return row, record, nil
}
