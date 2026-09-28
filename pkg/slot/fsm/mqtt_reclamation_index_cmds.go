package fsm

import (
	"bytes"
	"encoding/json"
	"io"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const cmdTypeMQTTReclamationIndex uint8 = 76

type mqttReclamationIndexCmd struct {
	result *metadb.MQTTReclamationIndexResult
}

func (c *mqttReclamationIndexCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	var err error
	c.result, err = wb.BuildMQTTReclamationIndex(hashSlot)
	return err
}
func (c *mqttReclamationIndexCmd) applyResult() []byte {
	data, _ := json.Marshal(c.result)
	return data
}

// EncodeMQTTReclamationIndexCommand advances one bounded page from the persisted
// primary cursor. The command's envelope selects one owned logical hash Slot.
func EncodeMQTTReclamationIndexCommand() []byte {
	return append([]byte{commandVersion, cmdTypeMQTTReclamationIndex}, []byte(`{"version":1}`)...)
}
func decodeMQTTReclamationIndexCommand(body []byte) (command, error) {
	if len(body) > 128-headerSize {
		return nil, metadb.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	var p struct {
		Version uint8 `json:"version"`
	}
	if err := d.Decode(&p); err != nil || p.Version != 1 {
		return nil, metadb.ErrInvalidArgument
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, metadb.ErrInvalidArgument
	}
	return &mqttReclamationIndexCmd{}, nil
}
