package fsm

import (
	"bytes"
	"encoding/json"
	"io"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	cmdTypeMQTTDeliveryCursorMutation uint8 = 82
	mqttDeliveryCursorCommandVersion        = 1
	maxMQTTDeliveryCursorCommandBytes       = 32 << 10
)

type mqttDeliveryCursorPayload struct {
	Version  uint8                             `json:"version"`
	Mutation metadb.MQTTDeliveryCursorMutation `json:"mutation"`
}

type mqttDeliveryCursorCmd struct {
	payload mqttDeliveryCursorPayload
	result  *metadb.MQTTDeliveryCursorResult
}

func (c *mqttDeliveryCursorCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	c.result, err = wb.MutateMQTTDeliveryCursor(hashSlot, c.payload.Mutation)
	return err
}

func (c *mqttDeliveryCursorCmd) applyResult() []byte {
	data, _ := json.Marshal(c.result)
	return data
}

// EncodeMQTTDeliveryCursorCommand preserves owner-fenced source coverage and
// backlog accounting. Route by the broker-scoped ClientID, not by the source.
// Storage cannot replace the caller's proof of protected committed coverage.
func EncodeMQTTDeliveryCursorCommand(m metadb.MQTTDeliveryCursorMutation) ([]byte, error) {
	if err := metadb.ValidateMQTTDeliveryCursorMutation(m); err != nil {
		return nil, err
	}
	payload := mqttDeliveryCursorPayload{Version: mqttDeliveryCursorCommandVersion, Mutation: m}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) > maxMQTTDeliveryCursorCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	return append([]byte{commandVersion, cmdTypeMQTTDeliveryCursorMutation}, body...), nil
}

func decodeMQTTDeliveryCursorCommand(data []byte) (command, error) {
	if len(data) > maxMQTTDeliveryCursorCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var payload mqttDeliveryCursorPayload
	if err := d.Decode(&payload); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	if payload.Version != mqttDeliveryCursorCommandVersion {
		return nil, metadb.ErrInvalidArgument
	}
	if err := metadb.ValidateMQTTDeliveryCursorMutation(payload.Mutation); err != nil {
		return nil, err
	}
	return &mqttDeliveryCursorCmd{payload: payload}, nil
}
