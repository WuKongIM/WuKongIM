package fsm

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestSubscriberCommandRejectsTooManyUIDs(t *testing.T) {
	uids := make([]string, MaxSubscriberCommandUIDs+1)
	for i := range uids {
		uids[i] = fmt.Sprintf("u%d", i)
	}

	_, err := EncodeAddSubscribersCommandChecked("g1", 2, uids)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)

	_, err = decodeCommand(EncodeAddSubscribersCommand("g1", 2, uids))
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestSubscriberCommandRejectsOversizedUIDBytes(t *testing.T) {
	uids := []string{strings.Repeat("u", MaxSubscriberCommandUIDBytes+1)}

	_, err := EncodeRemoveSubscribersCommandChecked("g1", 2, uids)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)

	_, err = decodeCommand(EncodeRemoveSubscribersCommand("g1", 2, uids))
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestSubscriberCommandAcceptsConfiguredLimit(t *testing.T) {
	uids := make([]string, MaxSubscriberCommandUIDs)
	for i := range uids {
		uids[i] = fmt.Sprintf("u%d", i)
	}

	cmd, err := EncodeAddSubscribersCommandChecked("g1", 2, uids)
	require.NoError(t, err)
	_, err = decodeCommand(cmd)
	require.NoError(t, err)
	require.False(t, errors.Is(err, metadb.ErrInvalidArgument))
}
