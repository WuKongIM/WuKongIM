package message

import (
	"bufio"
	"context"
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// BackupSnapshotBoundary is one complete Channel cursor encoded in a portable
// message snapshot.
type BackupSnapshotBoundary struct {
	ChannelKey     string
	ChannelID      string
	ChannelType    uint8
	Epoch          uint64
	LogStartOffset uint64
	HW             uint64
	FromExclusive  uint64
}

// BackupSnapshotRecord is one committed message row decoded from a portable
// message snapshot. Payload is valid only during the visitor.
type BackupSnapshotRecord struct {
	Boundary          BackupSnapshotBoundary
	MessageSeq        uint64
	MessageID         uint64
	Setting           uint8
	FromUID           string
	ClientMsgNo       string
	ServerTimestampMS int64
	SyncOnce          bool
	Payload           []byte
}

// ReplayBackupSnapshotReader validates and visits one seekable message stream.
// The boundary visitor runs before the records of its Channel.
func ReplayBackupSnapshotReader(
	ctx context.Context,
	reader io.ReadSeeker,
	size int64,
	visitBoundary func(BackupSnapshotBoundary) error,
	visitRecord func(BackupSnapshotRecord) error,
) (BackupSnapshotStats, error) {
	if visitBoundary == nil || visitRecord == nil {
		return BackupSnapshotStats{}, dberrors.ErrInvalidArgument
	}
	if err := verifyMessageBackupStreamChecksum(reader, size); err != nil {
		return BackupSnapshotStats{}, err
	}
	return parseMessageBackupStream(
		ctx, reader, size,
		func(
			ctx context.Context,
			buffer *bufio.Reader,
			header messageBackupChannelHeader,
		) (uint64, error) {
			boundary := BackupSnapshotBoundary{
				ChannelKey: string(header.key),
				ChannelID:  header.id.ID, ChannelType: header.id.Type,
				Epoch:          header.checkpoint.Epoch,
				LogStartOffset: header.checkpoint.LogStartOffset,
				HW:             header.checkpoint.HW,
				FromExclusive:  header.fromExclusive,
			}
			if err := visitBoundary(boundary); err != nil {
				return 0, err
			}
			var previousSeq uint64
			var maxMessageID uint64
			for index := uint64(0); index < header.messageCount; index++ {
				sequence, row, _, _, err := readMessageBackupStreamRow(
					ctx, buffer, header, previousSeq,
				)
				if err != nil {
					return 0, err
				}
				previousSeq = sequence
				if row.MessageID > maxMessageID {
					maxMessageID = row.MessageID
				}
				if err := visitRecord(BackupSnapshotRecord{
					Boundary:   boundary,
					MessageSeq: sequence, MessageID: row.MessageID,
					Setting: row.Setting, FromUID: row.FromUID,
					ClientMsgNo:       row.ClientMsgNo,
					ServerTimestampMS: row.ServerTimestampMS,
					SyncOnce:          row.FramerFlags&4 != 0,
					Payload:           row.Payload,
				}); err != nil {
					return 0, err
				}
			}
			return maxMessageID, nil
		},
	)
}
