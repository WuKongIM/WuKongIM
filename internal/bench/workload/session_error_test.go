package workload

import (
	"errors"
	"io"
	"strings"
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

func TestSessionErrorTextNeverExposesSessionOrMessageIdentity(t *testing.T) {
	const (
		uid         = "canary-uid-493857"
		channelID   = "canary-channel-291734"
		clientMsgNo = "canary-client-msg-875104"
	)
	err := &SessionError{
		UID:       uid,
		Operation: "group sendack",
		Err:       errors.New("send failed channel=" + channelID + " client_msg_no=" + clientMsgNo),
	}

	text := err.Error()
	for _, secret := range []string{uid, channelID, clientMsgNo} {
		require.NotContains(t, text, secret)
	}
	require.True(t, strings.Contains(text, "group sendack") || strings.Contains(text, "session operation"), text)
	require.Equal(t, []string{uid}, SessionErrorUIDs(err), "private recovery identity must remain available without entering text")
	require.ErrorIs(t, err, err.Err)
}
