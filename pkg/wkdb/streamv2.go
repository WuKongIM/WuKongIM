package wkdb

import (
	"strconv"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
)

type StreamV2 struct {
	MessageId   int64
	ChannelId   string
	ChannelType uint8
	FromUid     string
	End         uint8
	EndReason   uint8
	Payload     []byte
}

func (wk *wukongDB) SaveStreamV2(stream *StreamV2) error {

	db := wk.shardDB(strconv.FormatInt(stream.MessageId, 10))
	batch := db.NewBatch()
	defer batch.Close()

	if err := batch.Set(key.NewStreamV2ColumnKey(stream.MessageId, key.TableStreamV2.Column.ChannelId), []byte(stream.ChannelId), wk.noSync); err != nil {
		return err
	}

	if err := batch.Set(key.NewStreamV2ColumnKey(stream.MessageId, key.TableStreamV2.Column.ChannelType), []byte{stream.ChannelType}, wk.noSync); err != nil {
		return err
	}

	if err := batch.Set(key.NewStreamV2ColumnKey(stream.MessageId, key.TableStreamV2.Column.FromUid), []byte(stream.FromUid), wk.noSync); err != nil {
		return err
	}

	if err := batch.Set(key.NewStreamV2ColumnKey(stream.MessageId, key.TableStreamV2.Column.End), []byte{stream.End}, wk.noSync); err != nil {
		return err
	}
	if err := batch.Set(key.NewStreamV2ColumnKey(stream.MessageId, key.TableStreamV2.Column.EndReason), []byte{stream.EndReason}, wk.noSync); err != nil {
		return err
	}
	if err := batch.Set(key.NewStreamV2ColumnKey(stream.MessageId, key.TableStreamV2.Column.Payload), stream.Payload, wk.noSync); err != nil {
		return err
	}

	err := batch.Commit(wk.sync)
	if err != nil {
		return err
	}
	return nil
}

func (wk *wukongDB) GetStreamV2(messageId int64) (*StreamV2, error) {
	db := wk.shardDB(strconv.FormatInt(messageId, 10))
	iter := db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewStreamV2ColumnKey(messageId, key.MinColumnKey),
		UpperBound: key.NewStreamV2ColumnKey(messageId, key.MaxColumnKey),
	})
	defer iter.Close()

	streamV2 := &StreamV2{MessageId: messageId}
	hasData := false
	for iter.First(); iter.Valid(); iter.Next() {
		_, columnName, err := key.ParseStreamV2ColumnKey(iter.Key())
		if err != nil {
			return nil, err
		}
		switch columnName {
		case key.TableStreamV2.Column.ChannelId:
			streamV2.ChannelId = string(iter.Value())
		case key.TableStreamV2.Column.ChannelType:
			streamV2.ChannelType = iter.Value()[0]
		case key.TableStreamV2.Column.FromUid:
			streamV2.FromUid = string(iter.Value())
		case key.TableStreamV2.Column.End:
			streamV2.End = iter.Value()[0]
		case key.TableStreamV2.Column.EndReason:
			streamV2.EndReason = iter.Value()[0]
		case key.TableStreamV2.Column.Payload:
			// 这里必须复制一份，否则会被pebble覆盖
			var payload = make([]byte, len(iter.Value()))
			copy(payload, iter.Value())
			streamV2.Payload = payload
		}
		hasData = true
	}
	if !hasData {
		return nil, nil
	}

	return streamV2, nil
}

func (wk *wukongDB) GetStreamV2s(messageIds []int64) ([]*StreamV2, error) {
	streamV2s := make([]*StreamV2, 0, len(messageIds))
	for _, messageId := range messageIds {
		streamV2, err := wk.GetStreamV2(messageId)
		if err != nil {
			return nil, err
		}
		if streamV2 != nil {
			streamV2s = append(streamV2s, streamV2)
		}
	}
	return streamV2s, nil
}
