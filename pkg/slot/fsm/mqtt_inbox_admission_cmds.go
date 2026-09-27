package fsm

import (
	"bytes"
	"encoding/json"
	"io"
	"math"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	cmdTypeMQTTInboxAdmissionCAS      uint8 = 74
	mqttInboxAdmissionCommandVersion        = 1
	maxMQTTInboxAdmissionCommandBytes       = 32 << 10
)

type mqttInboxAdmissionCASPayload struct {
	Version          uint8                     `json:"version"`
	ExpectedRevision uint64                    `json:"expected_revision"`
	Admission        metadb.MQTTInboxAdmission `json:"admission"`
}

type mqttInboxAdmissionCASCmd struct {
	payload mqttInboxAdmissionCASPayload
	result  *metadb.MQTTInboxAdmissionResult
}

func (c *mqttInboxAdmissionCASCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	c.result, err = wb.CompareAndSwapMQTTInboxAdmission(hashSlot, c.payload.ExpectedRevision, c.payload.Admission)
	return err
}

func (c *mqttInboxAdmissionCASCmd) applyResult() []byte {
	data, _ := json.Marshal(c.result)
	return data
}

// EncodeMQTTInboxAdmissionCommand binds progress to the canonical person channel.
// Route by ChannelID; remote directory/source proofs remain caller obligations.
func EncodeMQTTInboxAdmissionCommand(expected uint64, row metadb.MQTTInboxAdmission) ([]byte, error) {
	payload := mqttInboxAdmissionCASPayload{Version: mqttInboxAdmissionCommandVersion, ExpectedRevision: expected, Admission: row}
	if err := validateMQTTInboxAdmissionCASPayload(payload); err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) > maxMQTTInboxAdmissionCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	return append([]byte{commandVersion, cmdTypeMQTTInboxAdmissionCAS}, body...), nil
}

func decodeMQTTInboxAdmissionCASCommand(data []byte) (command, error) {
	if len(data) > maxMQTTInboxAdmissionCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var payload mqttInboxAdmissionCASPayload
	if err := d.Decode(&payload); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	if err := validateMQTTInboxAdmissionCASPayload(payload); err != nil {
		return nil, err
	}
	return &mqttInboxAdmissionCASCmd{payload: payload}, nil
}

func validateMQTTInboxAdmissionCASPayload(p mqttInboxAdmissionCASPayload) error {
	if p.Version != mqttInboxAdmissionCommandVersion || p.ExpectedRevision == math.MaxUint64 || p.Admission.Revision != p.ExpectedRevision+1 || p.Admission.DirectoryGeneration == 0 {
		return metadb.ErrInvalidArgument
	}
	return metadb.ValidateMQTTInboxAdmission(p.Admission)
}
