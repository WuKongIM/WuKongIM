package fsm

import (
	"bytes"
	"encoding/json"
	"io"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	cmdTypeMQTTReplayMarkerClear uint8 = 78
	maxMQTTReplayMarkerBytes           = 4 << 10
)

type mqttReplayMarkerClearPayload struct {
	Version uint8                   `json:"version"`
	Owner   metadb.MQTTBindingOwner `json:"owner"`
}

type mqttReplayMarkerClearCmd struct {
	payload mqttReplayMarkerClearPayload
	result  *metadb.MQTTSourceBindingResult
}

func (c *mqttReplayMarkerClearCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	c.result, err = wb.ClearMQTTReplayMarker(hashSlot, c.payload.Owner)
	return err
}

func (c *mqttReplayMarkerClearCmd) applyResult() []byte {
	data, _ := json.Marshal(c.result)
	return data
}

// EncodeMQTTReplayMarkerClearCommand removes one Channel owner's replay marker.
// Route by the owner. Storage conflicts while any binding row remains; the use
// case must first prove replay cleanup has nothing left for this owner.
func EncodeMQTTReplayMarkerClearCommand(owner metadb.MQTTBindingOwner) ([]byte, error) {
	p := mqttReplayMarkerClearPayload{Version: 1, Owner: owner}
	if err := validateMQTTReplayMarkerClearPayload(p); err != nil {
		return nil, err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	if len(body) > maxMQTTReplayMarkerBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	return append([]byte{commandVersion, cmdTypeMQTTReplayMarkerClear}, body...), nil
}

func decodeMQTTReplayMarkerClearCommand(data []byte) (command, error) {
	if len(data) > maxMQTTReplayMarkerBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var p mqttReplayMarkerClearPayload
	if err := d.Decode(&p); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	if err := validateMQTTReplayMarkerClearPayload(p); err != nil {
		return nil, err
	}
	return &mqttReplayMarkerClearCmd{payload: p}, nil
}

func validateMQTTReplayMarkerClearPayload(p mqttReplayMarkerClearPayload) error {
	if p.Version != 1 || p.Owner.Kind != metadb.MQTTBindingChannel {
		return metadb.ErrInvalidArgument
	}
	return metadb.ValidateMQTTBindingOwner(p.Owner)
}
