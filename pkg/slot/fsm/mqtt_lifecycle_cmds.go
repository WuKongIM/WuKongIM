package fsm

import (
	"bytes"
	"encoding/json"
	"io"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	cmdTypeMQTTLifecycleMutation uint8 = 73
	mqttLifecycleCommandVersion        = 1
	maxMQTTLifecycleCommandBytes       = 256 << 10
)

type mqttLifecyclePayload struct {
	Version  uint8                        `json:"version"`
	Mutation metadb.MQTTLifecycleMutation `json:"mutation"`
}

type mqttLifecycleCmd struct {
	payload mqttLifecyclePayload
	result  *metadb.MQTTLifecycleResult
}

func (c *mqttLifecycleCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	c.result, err = wb.ApplyMQTTLifecycle(hashSlot, c.payload.Mutation)
	return err
}

func (c *mqttLifecycleCmd) applyResult() []byte {
	data, _ := json.Marshal(c.result)
	return data
}

// EncodeMQTTLifecycleCommand binds Session and old/new Will changes to one
// broker-scoped ClientID Slot. Authentication and old-owner isolation must be
// established by the caller before allowing the resulting connection to execute.
func EncodeMQTTLifecycleCommand(m metadb.MQTTLifecycleMutation) ([]byte, error) {
	if err := metadb.ValidateMQTTLifecycleMutation(m); err != nil {
		return nil, err
	}
	payload := mqttLifecyclePayload{Version: mqttLifecycleCommandVersion, Mutation: m}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) > maxMQTTLifecycleCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	return append([]byte{commandVersion, cmdTypeMQTTLifecycleMutation}, body...), nil
}

func decodeMQTTLifecycleCommand(data []byte) (command, error) {
	if len(data) > maxMQTTLifecycleCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var payload mqttLifecyclePayload
	if err := d.Decode(&payload); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	if payload.Version != mqttLifecycleCommandVersion {
		return nil, metadb.ErrInvalidArgument
	}
	if err := metadb.ValidateMQTTLifecycleMutation(payload.Mutation); err != nil {
		return nil, err
	}
	return &mqttLifecycleCmd{payload: payload}, nil
}
