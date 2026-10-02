package fsm

import (
	"bytes"
	"encoding/json"
	"io"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	cmdTypeMQTTSessionReclamation  uint8 = 75
	mqttReclamationCommandVersion        = 1
	maxMQTTReclamationCommandBytes       = 16 << 10
)

type mqttReclamationPayload struct {
	Version  uint8                         `json:"version"`
	Mutation metadb.MQTTSessionReclamation `json:"mutation"`
}

type mqttReclamationCmd struct {
	payload mqttReclamationPayload
	result  *metadb.MQTTSessionReclamationResult
}

func (c *mqttReclamationCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	c.result, err = wb.ReclaimMQTTSession(hashSlot, c.payload.Mutation)
	return err
}

func (c *mqttReclamationCmd) applyResult() []byte {
	data, _ := json.Marshal(c.result)
	return data
}

// EncodeMQTTSessionReclamationCommand bounds cleanup under the broker-scoped
// ClientID authority. The operation cannot retire source or Will obligations.
func EncodeMQTTSessionReclamationCommand(m metadb.MQTTSessionReclamation) ([]byte, error) {
	if err := metadb.ValidateMQTTSessionReclamation(m); err != nil {
		return nil, err
	}
	payload := mqttReclamationPayload{Version: mqttReclamationCommandVersion, Mutation: m}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) > maxMQTTReclamationCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	return append([]byte{commandVersion, cmdTypeMQTTSessionReclamation}, body...), nil
}

func decodeMQTTSessionReclamationCommand(data []byte) (command, error) {
	if len(data) > maxMQTTReclamationCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var payload mqttReclamationPayload
	if err := d.Decode(&payload); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	if payload.Version != mqttReclamationCommandVersion {
		return nil, metadb.ErrInvalidArgument
	}
	if err := metadb.ValidateMQTTSessionReclamation(payload.Mutation); err != nil {
		return nil, err
	}
	return &mqttReclamationCmd{payload: payload}, nil
}
