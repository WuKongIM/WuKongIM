package fsm

import (
	"bytes"
	"encoding/json"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"io"
)

const cmdTypeMQTTStorage uint8 = 79

type mqttStorageCmd struct {
	query  metadb.MQTTStorageAdjustment
	result *metadb.MQTTStorageResult
}

func (c *mqttStorageCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	c.result, err = wb.AdjustMQTTStorage(hashSlot, c.query)
	return err
}
func (c *mqttStorageCmd) applyResult() []byte { v, _ := json.Marshal(c.result); return v }

// EncodeMQTTStorageCommand routes one bounded adjustment by the constant
// cluster escrow identity, independently of message and Session authorities.
func EncodeMQTTStorageCommand(q metadb.MQTTStorageAdjustment) ([]byte, error) {
	if err := metadb.ValidateMQTTStorageAdjustment(q); err != nil {
		return nil, err
	}
	v, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}
	return append([]byte{commandVersion, cmdTypeMQTTStorage}, v...), nil
}
func decodeMQTTStorageCommand(v []byte) (command, error) {
	if len(v) > 32<<10 {
		return nil, metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(v))
	d.DisallowUnknownFields()
	var q metadb.MQTTStorageAdjustment
	if d.Decode(&q) != nil {
		return nil, metadb.ErrInvalidArgument
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	if err := metadb.ValidateMQTTStorageAdjustment(q); err != nil {
		return nil, err
	}
	return &mqttStorageCmd{query: q}, nil
}
