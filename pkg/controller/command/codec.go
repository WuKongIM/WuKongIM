package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const commandEnvelopeVersion uint32 = 1

// ErrUnsupportedVersion indicates that a command envelope version is unknown.
var ErrUnsupportedVersion = errors.New("controllerv2/command: unsupported version")

type envelope struct {
	Version uint32  `json:"version"`
	Command Command `json:"command"`
}

// Encode serializes cmd inside the current versioned JSON envelope.
func Encode(cmd Command) ([]byte, error) {
	return json.Marshal(envelope{Version: commandEnvelopeVersion, Command: cmd})
}

// Decode parses a versioned JSON command envelope.
func Decode(data []byte) (Command, error) {
	var env envelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&env); err != nil {
		return Command{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Command{}, fmt.Errorf("controllerv2/command: trailing JSON token")
		}
		return Command{}, err
	}
	if env.Version != commandEnvelopeVersion {
		return Command{}, fmt.Errorf("%w: %d", ErrUnsupportedVersion, env.Version)
	}
	return env.Command, nil
}
