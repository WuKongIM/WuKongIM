package fsm

import (
	"bytes"
	"encoding/json"
	"io"
	"math"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	cmdTypeMQTTSourceBindingCAS      uint8 = 71
	mqttSourceBindingCommandVersion        = 1
	maxMQTTSourceBindingCommandBytes       = 32 << 10
)

type mqttSourceBindingCASPayload struct {
	Version          uint8                    `json:"version"`
	ExpectedRevision uint64                   `json:"expected_revision"`
	Binding          metadb.MQTTSourceBinding `json:"binding"`
}

type mqttSourceBindingCASCmd struct {
	payload mqttSourceBindingCASPayload
	result  *metadb.MQTTSourceBindingResult
}

func (c *mqttSourceBindingCASCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	c.result, err = wb.CompareAndSwapMQTTSourceBinding(hashSlot, c.payload.ExpectedRevision, c.payload.Binding)
	return err
}

func (c *mqttSourceBindingCASCmd) applyResult() []byte {
	data, _ := json.Marshal(c.result)
	return data
}

// EncodeMQTTSourceBindingCommand preserves the complete row and expected revision.
// Route by the source Channel or UID owner, never by ClientID or source generation.
// The use case must establish current Session and replicated source proof before
// proposing progress or lifecycle changes; no Session row is required here.
func EncodeMQTTSourceBindingCommand(expected uint64, row metadb.MQTTSourceBinding) ([]byte, error) {
	payload := mqttSourceBindingCASPayload{Version: mqttSourceBindingCommandVersion, ExpectedRevision: expected, Binding: row}
	if err := validateMQTTSourceBindingCASPayload(payload); err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) > maxMQTTSourceBindingCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	return append([]byte{commandVersion, cmdTypeMQTTSourceBindingCAS}, body...), nil
}

func decodeMQTTSourceBindingCASCommand(data []byte) (command, error) {
	if len(data) > maxMQTTSourceBindingCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var payload mqttSourceBindingCASPayload
	if err := d.Decode(&payload); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	if err := validateMQTTSourceBindingCASPayload(payload); err != nil {
		return nil, err
	}
	return &mqttSourceBindingCASCmd{payload: payload}, nil
}

func validateMQTTSourceBindingCASPayload(p mqttSourceBindingCASPayload) error {
	if p.Version != mqttSourceBindingCommandVersion || p.ExpectedRevision == math.MaxUint64 || p.Binding.Revision != p.ExpectedRevision+1 {
		return metadb.ErrInvalidArgument
	}
	return metadb.ValidateMQTTSourceBinding(p.Binding)
}
