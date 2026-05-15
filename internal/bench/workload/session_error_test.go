package workload

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionErrorUIDsCollectsJoinedErrors(t *testing.T) {
	err := errors.Join(
		&SessionError{UID: "u-2", Operation: "recv", Err: io.EOF},
		&SessionError{UID: "u-1", Operation: "send", Err: io.EOF},
		&SessionError{UID: "u-2", Operation: "recvack", Err: io.EOF},
	)

	require.Equal(t, []string{"u-1", "u-2"}, SessionErrorUIDs(err))
}
