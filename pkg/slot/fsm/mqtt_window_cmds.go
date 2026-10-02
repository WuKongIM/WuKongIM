package fsm

import (
	"bytes"
	"encoding/json"
	"io"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	cmdTypeMQTTWindowMutation uint8 = 70
	mqttWindowCommandVersion        = 1
	maxMQTTWindowCommandBytes       = 32 << 10
)

type mqttWindowPayload struct {
	Version  uint8                     `json:"version"`
	Mutation metadb.MQTTWindowMutation `json:"mutation"`
}

type mqttWindowCmd struct {
	payload mqttWindowPayload
	result  *metadb.MQTTWindowResult
}

func (c *mqttWindowCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	c.result, err = wb.MutateMQTTWindow(hashSlot, c.payload.Mutation)
	return err
}

func (c *mqttWindowCmd) applyResult() []byte {
	data, _ := json.Marshal(c.result)
	return data
}

// EncodeMQTTWindowCommand persists exchanges before transmission and completes
// PUBACK with the source cursor and session counters in the same Slot commit.
// It does not prove current permission or authorize an unfenced socket to send.
func EncodeMQTTWindowCommand(m metadb.MQTTWindowMutation) ([]byte, error) {
	if err := metadb.ValidateMQTTWindowMutation(m); err != nil {
		return nil, err
	}
	payload := mqttWindowPayload{Version: mqttWindowCommandVersion, Mutation: m}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) > maxMQTTWindowCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	return append([]byte{commandVersion, cmdTypeMQTTWindowMutation}, body...), nil
}

func decodeMQTTWindowCommand(data []byte) (command, error) {
	if len(data) > maxMQTTWindowCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var payload mqttWindowPayload
	if err := d.Decode(&payload); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	if payload.Version != mqttWindowCommandVersion {
		return nil, metadb.ErrInvalidArgument
	}
	if err := metadb.ValidateMQTTWindowMutation(payload.Mutation); err != nil {
		return nil, err
	}
	return &mqttWindowCmd{payload: payload}, nil
}
