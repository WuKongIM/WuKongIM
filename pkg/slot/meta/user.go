package meta

import (
	"bytes"
	"context"
	"encoding/binary"

	"github.com/cockroachdb/pebble/v2"
)

type User struct {
	UID         string
	Token       string
	DeviceFlag  int64
	DeviceLevel int64
}

// UserCursor identifies the last emitted user in a shard page scan.
type UserCursor struct {
	// UID is the last emitted user ID in primary-key order.
	UID string
}

func (s *ShardStore) CreateUser(ctx context.Context, u User) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateUser(u); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	key := encodeUserPrimaryKey(s.slot, u.UID, userPrimaryFamilyID)
	exists, err := s.db.hasKey(key)
	if err != nil {
		return err
	}
	if exists {
		return ErrAlreadyExists
	}
	s.db.runAfterExistenceCheckHook()
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}

	value := encodeUserFamilyValue(u.Token, u.DeviceFlag, u.DeviceLevel, key)

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err := batch.Set(key, value, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) GetUser(ctx context.Context, uid string) (User, error) {
	if err := s.validate(); err != nil {
		return User{}, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return User{}, err
	}
	if uid == "" {
		return User{}, ErrInvalidArgument
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	return s.getUserLocked(uid)
}

func (s *ShardStore) getUserLocked(uid string) (User, error) {
	key := encodeUserPrimaryKey(s.slot, uid, userPrimaryFamilyID)
	value, err := s.db.getValue(key)
	if err != nil {
		return User{}, err
	}

	token, deviceFlag, deviceLevel, err := decodeUserFamilyValue(key, value)
	if err != nil {
		return User{}, err
	}

	return User{
		UID:         uid,
		Token:       token,
		DeviceFlag:  deviceFlag,
		DeviceLevel: deviceLevel,
	}, nil
}

// ListUsersPage scans one hash slot page in primary-key order.
func (s *ShardStore) ListUsersPage(ctx context.Context, after UserCursor, limit int) ([]User, UserCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, UserCursor{}, false, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return nil, UserCursor{}, false, err
	}
	if err := validateUserCursor(after); err != nil {
		return nil, UserCursor{}, false, err
	}
	if limit <= 0 {
		return nil, UserCursor{}, false, ErrInvalidArgument
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	prefix := encodeUserPrimaryPrefix(s.slot)
	lowerBound := prefix
	if after.UID != "" {
		lowerBound = nextPrefix(encodeUserPrimaryKey(s.slot, after.UID, userPrimaryFamilyID))
	}

	iter, err := s.db.db.NewIter(&pebble.IterOptions{
		LowerBound: lowerBound,
		UpperBound: nextPrefix(prefix),
	})
	if err != nil {
		return nil, UserCursor{}, false, err
	}
	defer iter.Close()

	users := make([]User, 0, limit+1)
	for ok := iter.SeekGE(lowerBound); ok; ok = iter.Next() {
		if err := s.db.checkContext(ctx); err != nil {
			return nil, UserCursor{}, false, err
		}

		key := iter.Key()
		if !bytes.HasPrefix(key, prefix) {
			break
		}
		value, err := iter.ValueAndErr()
		if err != nil {
			return nil, UserCursor{}, false, err
		}

		user, familyID, err := decodeUserRecord(key, value, prefix)
		if err != nil {
			return nil, UserCursor{}, false, err
		}
		if familyID != userPrimaryFamilyID {
			continue
		}

		users = append(users, user)
		if len(users) > limit {
			return users[:limit], userToCursor(users[limit-1]), false, nil
		}
	}
	if err := iter.Error(); err != nil {
		return nil, UserCursor{}, false, err
	}

	cursor := after
	if len(users) > 0 {
		cursor = userToCursor(users[len(users)-1])
	}
	return users, cursor, true, nil
}

func (s *ShardStore) UpdateUser(ctx context.Context, u User) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateUser(u); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	key := encodeUserPrimaryKey(s.slot, u.UID, userPrimaryFamilyID)
	exists, err := s.db.hasKey(key)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}

	value := encodeUserFamilyValue(u.Token, u.DeviceFlag, u.DeviceLevel, key)

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err := batch.Set(key, value, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) UpsertUser(ctx context.Context, u User) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if err := validateUser(u); err != nil {
		return err
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	key := encodeUserPrimaryKey(s.slot, u.UID, userPrimaryFamilyID)
	value := encodeUserFamilyValue(u.Token, u.DeviceFlag, u.DeviceLevel, key)

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err := batch.Set(key, value, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (s *ShardStore) DeleteUser(ctx context.Context, uid string) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}
	if uid == "" {
		return ErrInvalidArgument
	}

	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	key := encodeUserPrimaryKey(s.slot, uid, userPrimaryFamilyID)
	exists, err := s.db.hasKey(key)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if err := s.db.checkContext(ctx); err != nil {
		return err
	}

	batch := s.db.db.NewBatch()
	defer batch.Close()

	if err := batch.Delete(key, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func validateUser(u User) error {
	if u.UID == "" || len(u.UID) > maxKeyStringLen {
		return ErrInvalidArgument
	}
	return nil
}

func validateUserCursor(cursor UserCursor) error {
	if cursor.UID == "" {
		return nil
	}
	if len(cursor.UID) > maxKeyStringLen {
		return ErrInvalidArgument
	}
	return nil
}

func userToCursor(user User) UserCursor {
	return UserCursor{UID: user.UID}
}

func encodeUserPrimaryPrefix(hashSlot uint16) []byte {
	return encodeStatePrefix(hashSlot, UserTable.ID)
}

func decodeUserRecord(key, value, prefix []byte) (User, uint16, error) {
	rest := key[len(prefix):]
	uid, rest, err := decodeKeyString(rest)
	if err != nil {
		return User{}, 0, err
	}
	familyID, n := binary.Uvarint(rest)
	if n <= 0 {
		return User{}, 0, ErrCorruptValue
	}
	if len(rest[n:]) != 0 {
		return User{}, 0, ErrCorruptValue
	}

	token, deviceFlag, deviceLevel, err := decodeUserFamilyValue(key, value)
	if err != nil {
		return User{}, 0, err
	}
	return User{
		UID:         uid,
		Token:       token,
		DeviceFlag:  deviceFlag,
		DeviceLevel: deviceLevel,
	}, uint16(familyID), nil
}
