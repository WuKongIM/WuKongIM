package node

import (
	"encoding/binary"
	"errors"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
)

var errMQTTOwnerWire = errors.New("node: invalid MQTT owner frame")

// Frames carry only a complete owner identity. The distinct request/response
// magics, format 1, status and byte bounds are a closed compatibility contract.
const maxMQTTOwnerFrame = 2304

func encodeMQTTOwnerRequest(o contract.Owner) ([]byte, error) {
	return encodeMQTTOwnerFrame("WKMQ", 1, o)
}
func encodeMQTTOwnerResponse(o contract.Owner, status byte) ([]byte, error) {
	if status < 1 || status > 5 {
		return nil, errMQTTOwnerWire
	}
	return encodeMQTTOwnerFrame("WKMq", status, o)
}
func decodeMQTTOwnerRequest(body []byte) (contract.Owner, error) {
	o, op, err := decodeMQTTOwnerFrame(body, "WKMQ")
	if err != nil || op != 1 {
		return contract.Owner{}, errMQTTOwnerWire
	}
	return o, nil
}
func decodeMQTTOwnerResponse(body []byte) (contract.Owner, byte, error) {
	o, status, err := decodeMQTTOwnerFrame(body, "WKMq")
	if err != nil || status < 1 || status > 5 {
		return contract.Owner{}, 0, errMQTTOwnerWire
	}
	return o, status, nil
}

func encodeMQTTOwnerFrame(magic string, code byte, o contract.Owner) ([]byte, error) {
	if o.Validate() != nil {
		return nil, errMQTTOwnerWire
	}
	body := make([]byte, 0, 44+len(o.Key.Namespace)+len(o.Key.ClientID)+len(o.BootID))
	body = append(body, magic...)
	body = append(body, 1, code)
	text := func(s string) { body = binary.BigEndian.AppendUint16(body, uint16(len(s))); body = append(body, s...) }
	text(o.Key.Namespace)
	text(o.Key.ClientID)
	body = binary.BigEndian.AppendUint64(body, o.SessionGeneration)
	body = binary.BigEndian.AppendUint64(body, o.OwnerGeneration)
	body = binary.BigEndian.AppendUint64(body, o.NodeID)
	text(o.BootID)
	body = binary.BigEndian.AppendUint64(body, o.ConnectionID)
	if len(body) > maxMQTTOwnerFrame {
		return nil, errMQTTOwnerWire
	}
	return body, nil
}

func decodeMQTTOwnerFrame(body []byte, magic string) (contract.Owner, byte, error) {
	var o contract.Owner
	if len(body) < 6 || len(body) > maxMQTTOwnerFrame || string(body[:4]) != magic || body[4] != 1 {
		return o, 0, errMQTTOwnerWire
	}
	code := body[5]
	offset := 6
	valid := true
	text := func(limit int) string {
		if !valid || len(body)-offset < 2 {
			valid = false
			return ""
		}
		n := int(binary.BigEndian.Uint16(body[offset:]))
		offset += 2
		if n > limit || len(body)-offset < n {
			valid = false
			return ""
		}
		v := string(body[offset : offset+n])
		offset += n
		return v
	}
	number := func() uint64 {
		if !valid || len(body)-offset < 8 {
			valid = false
			return 0
		}
		v := binary.BigEndian.Uint64(body[offset:])
		offset += 8
		return v
	}
	o.Key.Namespace, o.Key.ClientID = text(1024), text(1024)
	o.SessionGeneration, o.OwnerGeneration, o.NodeID = number(), number(), number()
	o.BootID = text(128)
	o.ConnectionID = number()
	if !valid || offset != len(body) || o.Validate() != nil {
		return contract.Owner{}, 0, errMQTTOwnerWire
	}
	return o, code, nil
}
