package sendbanaudit

import (
	"context"
	"errors"
	"strings"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// Failure cases: timeouts report tentative state as committed; raw errors leak
// credentials; actor/target text is unbounded; omitted actor becomes invented.
func TestUnknownMutationOutcomeHasNoPolicyProof(t *testing.T) {
	var event Event
	observer := func(value Event) { event = value }
	ctx := WithActor(context.Background(), Actor{Source: "manager", Name: "verified-operator", Peer: "127.0.0.1"})
	RecordMutation(ctx, observer, metadb.SendBanMutation{UID: "user", SendBan: 1}, metadb.SendBanResult{
		Status: "ok", SendBan: 1, Version: 4, Previous: &metadb.SendBanPolicy{SendBan: 0, Version: 3},
	}, errors.New("private-error-with-token"))
	require.Equal(t, "outcome_unknown", event.Result)
	require.Nil(t, event.Previous)
	require.Nil(t, event.Current)
	require.Equal(t, "verified-operator", event.Actor.Name)
	require.Equal(t, "manager", event.Actor.Source)

	RecordMutation(context.Background(), observer, metadb.SendBanMutation{UID: strings.Repeat("x", 4096), SendBan: 1}, metadb.SendBanResult{}, metadb.ErrInvalidArgument)
	require.Equal(t, "invalid_request", event.Result)
	require.Equal(t, "unknown", event.Actor.Name)
	require.LessOrEqual(t, len(event.UID), 512)
	RecordMutation(nil, nil, metadb.SendBanMutation{}, metadb.SendBanResult{}, nil)
}
