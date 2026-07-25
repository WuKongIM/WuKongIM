package backup

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"unicode/utf8"

	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type messageSourceIdentity struct {
	channelID   string
	channelType uint8
}

func messageChannelRequest(hashSlot uint16, meta metadb.ChannelRuntimeMeta) (clusterpkg.BackupMessageChannelRequest, error) {
	if meta.ChannelID == "" || len(meta.ChannelID) > 4<<10 || !utf8.ValidString(meta.ChannelID) ||
		meta.ChannelType < 0 || meta.ChannelType > math.MaxUint8 ||
		meta.ChannelEpoch == 0 || meta.LeaderEpoch == 0 || meta.Leader == 0 || meta.MinISR <= 0 ||
		meta.MinISR > math.MaxInt {
		return clusterpkg.BackupMessageChannelRequest{}, runtimebackup.ErrInvalidCapture
	}
	return clusterpkg.BackupMessageChannelRequest{
		HashSlot: hashSlot, ChannelID: meta.ChannelID, ChannelType: uint8(meta.ChannelType),
		LeaderNodeID: meta.Leader, ChannelEpoch: meta.ChannelEpoch,
		LeaderEpoch: meta.LeaderEpoch, MinISR: int(meta.MinISR),
		RetentionSeq: meta.RetentionThroughSeq,
	}, nil
}

func artifactBoundary(boundary clusterpkg.BackupMessageChannelBoundary) backupartifact.ChannelBoundary {
	return backupartifact.ChannelBoundary{
		ChannelID: boundary.ChannelID, ChannelType: boundary.ChannelType,
		Epoch: boundary.Epoch, LogStartOffset: boundary.LogStartOffset, HW: boundary.HW,
	}
}

func appendMessageLogSourcePage(page *runtimebackup.SourcePage, source clusterpkg.BackupMessageLogPage, currentBytes, targetBytes int64) (int, int64, error) {
	if len(source.Records) == 0 || source.NextSeq == 0 {
		return 0, 0, runtimebackup.ErrInvalidCapture
	}
	var addedBytes int64
	added := 0
	for _, record := range source.Records {
		recordBytes := int64(4 + len(record))
		if len(page.Records)+added > 0 && currentBytes+addedBytes > targetBytes-recordBytes {
			break
		}
		page.Records = append(page.Records, record)
		added++
		addedBytes += recordBytes
	}
	if added == 0 {
		return 0, 0, nil
	}
	if added != len(source.Records) {
		return 0, 0, runtimebackup.ErrInvalidCapture
	}
	page.MessageCursors = append(page.MessageCursors, artifactBoundary(source.Boundary))
	return added, addedBytes, nil
}

type messageSourceCursor struct {
	Version        uint8                                    `json:"v"`
	BasePosition   uint64                                   `json:"base"`
	TargetPosition uint64                                   `json:"target"`
	Consumed       uint64                                   `json:"consumed,omitempty"`
	After          metadb.ChannelRuntimeMetaCursor          `json:"after,omitempty"`
	Boundary       *clusterpkg.BackupMessageChannelBoundary `json:"boundary,omitempty"`
	PreviousEpoch  uint64                                   `json:"previous_epoch,omitempty"`
	PreviousStart  uint64                                   `json:"previous_start,omitempty"`
	PreviousHW     uint64                                   `json:"previous_hw,omitempty"`
	NextSeq        uint64                                   `json:"next_seq,omitempty"`
}

func parseMessageSourceCursor(encoded, throughCursor string, through uint64) (messageSourceCursor, error) {
	cursor, err := decodeMessageSourceCursor(encoded)
	if err != nil {
		return messageSourceCursor{}, err
	}
	cut, err := decodeMessageSourceCursor(throughCursor)
	if err != nil {
		return messageSourceCursor{}, err
	}
	if cut.TargetPosition != through || !cut.active() {
		return messageSourceCursor{}, runtimebackup.ErrInvalidCapture
	}
	if !cursor.active() {
		if cursor.position() != cut.BasePosition {
			return messageSourceCursor{}, runtimebackup.ErrInvalidCapture
		}
		return cut, nil
	}
	if !cursor.sameTarget(cut) || cursor.position() < cut.position() {
		return messageSourceCursor{}, runtimebackup.ErrInvalidCapture
	}
	return cursor, nil
}

func decodeMessageSourceCursor(encoded string) (messageSourceCursor, error) {
	cursor := messageSourceCursor{Version: messageSourceCursorVersion}
	if encoded == "" {
		return cursor, nil
	}
	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(body) > 8<<10 {
		return messageSourceCursor{}, runtimebackup.ErrInvalidCapture
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return messageSourceCursor{}, runtimebackup.ErrInvalidCapture
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return messageSourceCursor{}, runtimebackup.ErrInvalidCapture
	}
	if cursor.Version != messageSourceCursorVersion ||
		cursor.BasePosition > cursor.TargetPosition ||
		cursor.Consumed > cursor.TargetPosition-cursor.BasePosition {
		return messageSourceCursor{}, runtimebackup.ErrInvalidCapture
	}
	if cursor.active() {
		if cursor.Boundary == nil ||
			cursor.Boundary.ChannelID == "" ||
			len(cursor.Boundary.ChannelID) > 4<<10 ||
			!utf8.ValidString(cursor.Boundary.ChannelID) ||
			cursor.Boundary.Epoch == 0 ||
			cursor.Boundary.HW == math.MaxUint64 ||
			cursor.Boundary.LogStartOffset > cursor.Boundary.HW ||
			cursor.Boundary.ObservedAtUnixMillis <= 0 ||
			(cursor.PreviousEpoch == 0 &&
				(cursor.PreviousStart != 0 || cursor.PreviousHW != 0)) ||
			(cursor.PreviousEpoch > 0 &&
				cursor.PreviousStart > cursor.PreviousHW) ||
			cursor.After != (metadb.ChannelRuntimeMetaCursor{}) ||
			(cursor.NextSeq > 0 && cursor.NextSeq > cursor.Boundary.HW+1) {
			return messageSourceCursor{}, runtimebackup.ErrInvalidCapture
		}
	} else if cursor.Boundary != nil || cursor.NextSeq != 0 ||
		cursor.PreviousEpoch != 0 || cursor.PreviousStart != 0 || cursor.PreviousHW != 0 ||
		cursor.BasePosition != cursor.TargetPosition || cursor.Consumed != 0 {
		return messageSourceCursor{}, runtimebackup.ErrInvalidCapture
	}
	return cursor, nil
}

func marshalMessageSourceCursor(cursor messageSourceCursor) (string, error) {
	body, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(body)
	if len(encoded) > 8<<10 {
		return "", runtimebackup.ErrInvalidCapture
	}
	return encoded, nil
}

func (cursor messageSourceCursor) active() bool {
	return cursor.TargetPosition > cursor.position()
}

func (cursor messageSourceCursor) position() uint64 {
	return cursor.BasePosition + cursor.Consumed
}

func (cursor messageSourceCursor) sameTarget(other messageSourceCursor) bool {
	if cursor.BasePosition != other.BasePosition ||
		cursor.TargetPosition != other.TargetPosition ||
		cursor.Boundary == nil || other.Boundary == nil {
		return false
	}
	return *cursor.Boundary == *other.Boundary
}
