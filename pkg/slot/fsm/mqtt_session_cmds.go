package fsm

import (
	"bytes"
	"encoding/json"
	"io"
	"math"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	cmdTypeMQTTSessionCAS      uint8 = 67
	mqttSessionCommandVersion        = 1
	maxMQTTSessionCommandBytes       = 32 << 10
)

type mqttSessionCASPayload struct {
	Version          uint8              `json:"version"`
	ExpectedRevision uint64             `json:"expected_revision"`
	Session          metadb.MQTTSession `json:"session"`
}

type mqttSessionCASCmd struct {
	payload mqttSessionCASPayload
	result  *metadb.MQTTSessionCASResult
}

func (c *mqttSessionCASCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	c.result, err = wb.CompareAndSwapMQTTSession(hashSlot, c.payload.ExpectedRevision, c.payload.Session)
	return err
}

func (c *mqttSessionCASCmd) applyResult() []byte {
	data, _ := json.Marshal(c.result)
	return data
}

// EncodeMQTTSessionCASCommand preserves the complete row and expected revision.
// Callers must route by the broker-scoped ClientID and prove the old owner fenced
// before using this storage mutation to install a new execution owner.
func EncodeMQTTSessionCASCommand(expected uint64, row metadb.MQTTSession) ([]byte, error) {
	payload := mqttSessionCASPayload{Version: mqttSessionCommandVersion, ExpectedRevision: expected, Session: row}
	if err := validateMQTTSessionCASPayload(payload); err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) > maxMQTTSessionCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	return append([]byte{commandVersion, cmdTypeMQTTSessionCAS}, body...), nil
}

func decodeMQTTSessionCASCommand(data []byte) (command, error) {
	if len(data) > maxMQTTSessionCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var payload mqttSessionCASPayload
	if err := d.Decode(&payload); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	if err := validateMQTTSessionCASPayload(payload); err != nil {
		return nil, err
	}
	return &mqttSessionCASCmd{payload: payload}, nil
}

func validateMQTTSessionCASPayload(p mqttSessionCASPayload) error {
	if p.Version != mqttSessionCommandVersion || p.ExpectedRevision == math.MaxUint64 || p.Session.Revision != p.ExpectedRevision+1 {
		return metadb.ErrInvalidArgument
	}
	return metadb.ValidateMQTTSession(p.Session)
}
