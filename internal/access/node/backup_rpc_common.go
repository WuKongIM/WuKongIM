package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const maxScheduledBackupRPCBytes = 8 << 20

func encodeBackupJSON(magic []byte, value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(payload)+len(magic) > maxScheduledBackupRPCBytes {
		return nil, fmt.Errorf("internal/access/node: backup RPC payload exceeds limit")
	}
	return append(append([]byte(nil), magic...), payload...), nil
}

func decodeBackupJSON(body, magic []byte, target any) error {
	if len(body) > maxScheduledBackupRPCBytes || !hasMagic(body, magic) {
		return fmt.Errorf("internal/access/node: invalid backup RPC codec")
	}
	decoder := json.NewDecoder(bytes.NewReader(body[len(magic):]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("internal/access/node: trailing backup RPC data")
	}
	return nil
}

func backupMessageStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	default:
		return rpcStatusRejected
	}
}

func backupMessageErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusRejected:
		return fmt.Errorf("scheduled backup node operation rejected")
	default:
		return fmt.Errorf(
			"internal/access/node: unknown backup RPC status %q", status,
		)
	}
}
