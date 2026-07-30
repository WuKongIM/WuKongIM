package cluster

import (
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestChannelMigrationRemoteErrorMapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "stale meta", err: transport.RemoteError{Code: "remote_error", Message: metadb.ErrStaleMeta.Error()}, want: metadb.ErrStaleMeta},
		{name: "not leader", err: transport.RemoteError{Code: "remote_error", Message: ErrNotLeader.Error()}, want: ErrNotLeader},
		{name: "invalid argument", err: transport.RemoteError{Code: "remote_error", Message: metadb.ErrInvalidArgument.Error()}, want: metadb.ErrInvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := mapChannelMigrationRemoteError(tc.err)
			if !errors.Is(err, tc.want) {
				t.Fatalf("mapChannelMigrationRemoteError() = %v, want %v", err, tc.want)
			}
		})
	}
}
