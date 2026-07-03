package app

import (
	"crypto/rand"
	"encoding/binary"
)

func newGatewayBootID() (uint64, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, err
	}
	id := binary.BigEndian.Uint64(raw[:])
	if id == 0 {
		return 1, nil
	}
	return id, nil
}
