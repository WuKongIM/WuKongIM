package cluster

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestPersonDirectoryMembershipWriterPreservesAlignedPartialResults(t *testing.T) {
	wantErr := errors.New("second UID unavailable")
	writer := NewPersonDirectoryMembershipWriter(personDirectoryMembershipNodeStub{errs: []error{nil, wantErr}})
	results := writer.EnsureUserChannelMembershipBatch(context.Background(), []metadb.UserChannelMembership{{UID: "u1"}, {UID: "u2"}})
	if len(results) != 2 || results[0].Err != nil || !errors.Is(results[1].Err, wantErr) {
		t.Fatalf("results = %#v, want aligned partial failure", results)
	}
}

type personDirectoryMembershipNodeStub struct{ errs []error }

func (s personDirectoryMembershipNodeStub) EnsureUserChannelMembershipBatch(context.Context, []metadb.UserChannelMembership) []error {
	return append([]error(nil), s.errs...)
}
