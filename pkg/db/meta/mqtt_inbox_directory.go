package meta

import (
	"cmp"
	"context"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// CompareMQTTDirectoryKeys follows the membership primary key's durable order,
// independent of mutable conversation activation, visibility or membership state.
func CompareMQTTDirectoryKeys(a, b ChannelKey) int {
	if n := cmp.Compare(len(a.ChannelID), len(b.ChannelID)); n != 0 {
		return n
	}
	if n := strings.Compare(a.ChannelID, b.ChannelID); n != 0 {
		return n
	}
	return cmp.Compare(a.ChannelType, b.ChannelType)
}

func validateMQTTDirectoryKey(k ChannelKey) error {
	if validateMQTTIdentity(k.ChannelID, 4096) != nil || k.ChannelType < 1 || k.ChannelType > 255 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// readMQTTInboxDirectory uses the enclosing read's pinned snapshot. The strict
// primary scan visits at most limit+1 rows; all types and tombstones consume that
// budget. These keys are discovery candidates, never a receive or retention grant.
func (s *Shard) readMQTTInboxDirectory(ctx context.Context, uid string, after ChannelKey, limit int) ([]ChannelKey, ChannelKey, bool, error) {
	var cursor KeyParts
	if after != (ChannelKey{}) {
		cursor = userChannelMembershipPrimaryKey(uid, after.ChannelID, after.ChannelType)
	}
	rows, _, done, err := userChannelMembershipTable.scanPrimaryPrefixStrict(ctx, s, KeyParts{String(uid)}, cursor, limit)
	if err != nil {
		return nil, after, false, err
	}
	if err = contextErr(ctx); err != nil {
		return nil, after, false, err
	}
	keys := make([]ChannelKey, 0, len(rows))
	last := after
	for _, row := range rows {
		key := ChannelKey{ChannelID: row.ChannelID, ChannelType: row.ChannelType}
		if row.UID != uid || validateMQTTDirectoryKey(key) != nil || (last != (ChannelKey{}) && CompareMQTTDirectoryKeys(last, key) >= 0) {
			return nil, after, false, dberrors.ErrCorruptValue
		}
		keys = append(keys, key)
		last = key
	}
	return keys, last, done, nil
}
