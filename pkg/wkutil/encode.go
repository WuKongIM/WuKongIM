package wkutil

import (
	"bytes"
	"encoding/gob"
)

// EncodeToBytes encodes an interface{} into a byte slice.
func EncodeToBytes(data interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeFromBytes decodes a byte slice into an interface{}.
func DecodeFromBytes(data []byte, v interface{}) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)
	return dec.Decode(v)
}
