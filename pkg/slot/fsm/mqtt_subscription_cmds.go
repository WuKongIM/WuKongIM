package fsm

import (
	"bytes"
	"encoding/json"
	"io"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	cmdTypeMQTTSubscriptionMutation uint8 = 68
	mqttSubscriptionCommandVersion        = 1
	maxMQTTSubscriptionCommandBytes       = 32 << 10
)

type mqttSubscriptionPayload struct {
	Version  uint8                           `json:"version"`
	Mutation metadb.MQTTSubscriptionMutation `json:"mutation"`
}

type mqttSubscriptionCmd struct {
	payload mqttSubscriptionPayload
	result  *metadb.MQTTSessionCASResult
}

func (c *mqttSubscriptionCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	c.result, err = wb.MutateMQTTSubscription(hashSlot, c.payload.Mutation)
	return err
}

func (c *mqttSubscriptionCmd) applyResult() []byte {
	data, _ := json.Marshal(c.result)
	return data
}

// EncodeMQTTSubscriptionCommand binds intent changes to the initiating owner and
// session revision. Route by the broker-scoped ClientID, not by the topic target.
// Active stage requires the use case's separate source-protection proof.
func EncodeMQTTSubscriptionCommand(m metadb.MQTTSubscriptionMutation) ([]byte, error) {
	if err := metadb.ValidateMQTTSubscriptionMutation(m); err != nil {
		return nil, err
	}
	payload := mqttSubscriptionPayload{Version: mqttSubscriptionCommandVersion, Mutation: m}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) > maxMQTTSubscriptionCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	return append([]byte{commandVersion, cmdTypeMQTTSubscriptionMutation}, body...), nil
}

func decodeMQTTSubscriptionCommand(data []byte) (command, error) {
	if len(data) > maxMQTTSubscriptionCommandBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var payload mqttSubscriptionPayload
	if err := d.Decode(&payload); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	if payload.Version != mqttSubscriptionCommandVersion {
		return nil, metadb.ErrInvalidArgument
	}
	if err := metadb.ValidateMQTTSubscriptionMutation(payload.Mutation); err != nil {
		return nil, err
	}
	return &mqttSubscriptionCmd{payload: payload}, nil
}
