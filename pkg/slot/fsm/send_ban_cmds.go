package fsm

import (
	"bytes"
	"encoding/json"
	"io"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const cmdTypeSetSendBan uint8 = 67

type sendBanCmd struct {
	mutation metadb.SendBanMutation
	result   *metadb.SendBanResult
}

func (c *sendBanCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	c.result, err = wb.ApplySendBan(hashSlot, c.mutation)
	return err
}
func (c *sendBanCmd) applyResult() []byte { b, _ := json.Marshal(c.result); return b }

// EncodeSendBanCommand carries one bounded, atomic user or Channel policy update.
func EncodeSendBanCommand(q metadb.SendBanMutation) ([]byte, error) {
	if err := metadb.ValidateSendBanMutation(q); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}
	if len(raw) > 4096 {
		return nil, metadb.ErrInvalidArgument
	}
	return append([]byte{commandVersion, cmdTypeSetSendBan}, raw...), nil
}
func decodeSendBanCommand(data []byte) (command, error) {
	if len(data) > 4096 {
		return nil, metadb.ErrInvalidArgument
	}
	var q metadb.SendBanMutation
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&q); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	if err := metadb.ValidateSendBanMutation(q); err != nil {
		return nil, err
	}
	return &sendBanCmd{mutation: q}, nil
}

const cmdTypeChannelInfo uint8 = 68

type channelInfoCmd struct {
	mutation metadb.ChannelInfoMutation
	result   *metadb.SendBanResult
}

func (c *channelInfoCmd) apply(wb *metadb.WriteBatch, hs uint16) error {
	var err error
	c.result, err = wb.ApplyChannelInfo(hs, c.mutation)
	return err
}
func (c *channelInfoCmd) applyResult() []byte { b, _ := json.Marshal(c.result); return b }

// EncodeChannelInfoCommand preserves omission of the optional sending policy.
func EncodeChannelInfoCommand(q metadb.ChannelInfoMutation) ([]byte, error) {
	if err := metadb.ValidateChannelInfoMutation(q); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}
	if len(raw) > 4096 {
		return nil, metadb.ErrInvalidArgument
	}
	return append([]byte{commandVersion, cmdTypeChannelInfo}, raw...), nil
}
func decodeChannelInfoCommand(data []byte) (command, error) {
	if len(data) > 4096 {
		return nil, metadb.ErrInvalidArgument
	}
	var q metadb.ChannelInfoMutation
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&q); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	if err := metadb.ValidateChannelInfoMutation(q); err != nil {
		return nil, err
	}
	return &channelInfoCmd{mutation: q}, nil
}
