package fsm

import (
	"bytes"
	"encoding/json"
	"io"
	"math"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	cmdTypeMQTTWillCAS      uint8 = 72
	mqttWillCommandVersion        = 1
	maxMQTTWillCommandBytes       = 256 << 10
)

type mqttWillCASPayload struct {
	Version          uint8           `json:"version"`
	ExpectedRevision uint64          `json:"expected_revision"`
	Will             metadb.MQTTWill `json:"will"`
}

type mqttWillCASCmd struct {
	payload mqttWillCASPayload
	result  *metadb.MQTTWillResult
}

func (c *mqttWillCASCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	c.result, err = wb.CompareAndSwapMQTTWill(hashSlot, c.payload.ExpectedRevision, c.payload.Will)
	return err
}

func (c *mqttWillCASCmd) applyResult() []byte {
	data, _ := json.Marshal(c.result)
	return data
}

// EncodeMQTTWillCommand preserves the complete row and expected revision.
// Route by the broker-scoped ClientID. Current authorization and atomic Session
// lifecycle decisions remain separate requirements before product activation.
// This CAS command preserves old obligations after the live Session is replaced.
func EncodeMQTTWillCommand(expected uint64, row metadb.MQTTWill) ([]byte, error) {
	payload := mqttWillCASPayload{Version: mqttWillCommandVersion, ExpectedRevision: expected, Will: row}
	if err := validateMQTTWillCASPayload(payload); err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) > maxMQTTWillCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	return append([]byte{commandVersion, cmdTypeMQTTWillCAS}, body...), nil
}

func decodeMQTTWillCASCommand(data []byte) (command, error) {
	if len(data) > maxMQTTWillCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var payload mqttWillCASPayload
	if err := d.Decode(&payload); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	if err := validateMQTTWillCASPayload(payload); err != nil {
		return nil, err
	}
	return &mqttWillCASCmd{payload: payload}, nil
}

func validateMQTTWillCASPayload(p mqttWillCASPayload) error {
	if p.Version != mqttWillCommandVersion || p.ExpectedRevision == math.MaxUint64 || p.Will.Revision != p.ExpectedRevision+1 {
		return metadb.ErrInvalidArgument
	}
	return metadb.ValidateMQTTWill(p.Will)
}
