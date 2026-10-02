package fsm

import (
	"bytes"
	"encoding/json"
	"io"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	cmdTypeMQTTSourceBindingRetire  uint8 = 77
	maxMQTTSourceBindingRetireBytes       = 8 << 10
)

type mqttSourceBindingRetirePayload struct {
	Version          uint8                       `json:"version"`
	Key              metadb.MQTTSourceBindingKey `json:"key"`
	ExpectedRevision uint64                      `json:"expected_revision"`
	// ClosedThrough is the Session generation the caller proved has ended.
	ClosedThrough uint64 `json:"closed_through,omitempty"`
	// LiveSubscriptionThrough is the subscription generation through which the
	// caller proved every subscription of the still-live Session was Removed.
	// Exactly one of ClosedThrough and LiveSubscriptionThrough is non-zero.
	LiveSubscriptionThrough uint64 `json:"live_subscription_through,omitempty"`
}

type mqttSourceBindingRetireCmd struct {
	payload mqttSourceBindingRetirePayload
	result  *metadb.MQTTSourceBindingResult
}

func (c *mqttSourceBindingRetireCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	if c.payload.LiveSubscriptionThrough != 0 {
		c.result, err = wb.RetireLiveMQTTSourceBinding(hashSlot, c.payload.Key, c.payload.ExpectedRevision, c.payload.LiveSubscriptionThrough)
		return err
	}
	c.result, err = wb.RetireMQTTSourceBinding(hashSlot, c.payload.Key, c.payload.ExpectedRevision, c.payload.ClosedThrough)
	return err
}

func (c *mqttSourceBindingRetireCmd) applyResult() []byte {
	data, _ := json.Marshal(c.result)
	return data
}

// EncodeMQTTSourceBindingRetireCommand deletes one acknowledged Removed tombstone
// and raises its closed-lifetime fence. Route by the binding owner. The use case
// must first read the Session Slot proving closedThrough has ended.
func EncodeMQTTSourceBindingRetireCommand(key metadb.MQTTSourceBindingKey, expected, closedThrough uint64) ([]byte, error) {
	p := mqttSourceBindingRetirePayload{Version: 1, Key: key, ExpectedRevision: expected, ClosedThrough: closedThrough}
	if err := validateMQTTSourceBindingRetirePayload(p); err != nil {
		return nil, err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	if len(body) > maxMQTTSourceBindingRetireBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	return append([]byte{commandVersion, cmdTypeMQTTSourceBindingRetire}, body...), nil
}

// EncodeMQTTSourceBindingLiveRetireCommand deletes one acknowledged Removed
// tombstone of a still-live Session. The use case must first read the Session
// Slot proving the lifetime is current and every subscription through
// subscriptionThrough is Removed.
func EncodeMQTTSourceBindingLiveRetireCommand(key metadb.MQTTSourceBindingKey, expected, subscriptionThrough uint64) ([]byte, error) {
	p := mqttSourceBindingRetirePayload{Version: 1, Key: key, ExpectedRevision: expected, LiveSubscriptionThrough: subscriptionThrough}
	if err := validateMQTTSourceBindingRetirePayload(p); err != nil {
		return nil, err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return append([]byte{commandVersion, cmdTypeMQTTSourceBindingRetire}, body...), nil
}

func decodeMQTTSourceBindingRetireCommand(data []byte) (command, error) {
	if len(data) > maxMQTTSourceBindingRetireBytes-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var p mqttSourceBindingRetirePayload
	if err := d.Decode(&p); err != nil {
		return nil, metadb.ErrInvalidArgument
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	if err := validateMQTTSourceBindingRetirePayload(p); err != nil {
		return nil, err
	}
	return &mqttSourceBindingRetireCmd{payload: p}, nil
}

func validateMQTTSourceBindingRetirePayload(p mqttSourceBindingRetirePayload) error {
	if p.Version != 1 || p.ExpectedRevision == 0 || p.Key.SessionGeneration == 0 || (p.ClosedThrough == 0) == (p.LiveSubscriptionThrough == 0) ||
		p.ClosedThrough != 0 && p.ClosedThrough < p.Key.SessionGeneration || p.LiveSubscriptionThrough != 0 && p.LiveSubscriptionThrough < p.Key.SubscriptionGeneration {
		return metadb.ErrInvalidArgument
	}
	return metadb.ValidateMQTTSourceBindingKey(p.Key)
}
