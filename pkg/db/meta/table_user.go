package meta

import (
	"bytes"
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// User stores token defaults for a UID.
type User struct {
	UID         string
	Token       string
	DeviceFlag  int64
	DeviceLevel int64
}

// CreateUser inserts a user and rejects duplicates.
func (s *Shard) CreateUser(ctx context.Context, user User) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateKeyString(user.UID); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	key := encodeUserRowKey(s.hashSlot, user.UID, userPrimaryFamilyID)
	if _, ok, err := s.db.get(key); err != nil || ok {
		if err != nil {
			return err
		}
		return dberrors.ErrAlreadyExists
	}
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	return commitSet(batch, key, encodeUserValue(user))
}

// UpsertUser stores a user regardless of prior existence.
func (s *Shard) UpsertUser(ctx context.Context, user User) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateKeyString(user.UID); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	return commitSet(batch, encodeUserRowKey(s.hashSlot, user.UID, userPrimaryFamilyID), encodeUserValue(user))
}

// UpdateUser updates an existing user.
func (s *Shard) UpdateUser(ctx context.Context, user User) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateKeyString(user.UID); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	key := encodeUserRowKey(s.hashSlot, user.UID, userPrimaryFamilyID)
	if _, ok, err := s.db.get(key); err != nil || !ok {
		if err != nil {
			return err
		}
		return dberrors.ErrNotFound
	}
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	return commitSet(batch, key, encodeUserValue(user))
}

// GetUser returns one user by UID.
func (s *Shard) GetUser(ctx context.Context, uid string) (User, bool, error) {
	if err := s.check(ctx); err != nil {
		return User{}, false, err
	}
	if err := validateKeyString(uid); err != nil {
		return User{}, false, err
	}
	key := encodeUserRowKey(s.hashSlot, uid, userPrimaryFamilyID)
	value, ok, err := s.db.get(key)
	if err != nil || !ok {
		return User{}, ok, err
	}
	user, err := decodeUserValue(uid, value)
	return user, err == nil, err
}

// DeleteUser removes one user by UID.
func (s *Shard) DeleteUser(ctx context.Context, uid string) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if err := validateKeyString(uid); err != nil {
		return err
	}
	unlock := s.lock()
	defer unlock()
	batch := s.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Delete(encodeUserRowKey(s.hashSlot, uid, userPrimaryFamilyID)); err != nil {
		return err
	}
	return batch.Commit(true)
}

// ListUsersPage returns users after cursorUID in ascending UID order.
func (s *Shard) ListUsersPage(ctx context.Context, cursorUID string, limit int) ([]User, string, bool, error) {
	if err := s.check(ctx); err != nil {
		return nil, "", false, err
	}
	if limit <= 0 {
		return nil, "", true, nil
	}
	prefix := encodeRowPrefix(s.hashSlot, TableIDUser)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := s.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return nil, "", false, err
	}
	defer iter.Close()
	users := make([]User, 0, limit)
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, "", false, err
		}
		uid, ok := decodeUserKeyUID(prefix, iter.Key())
		if !ok || uid <= cursorUID {
			continue
		}
		value, err := iter.Value()
		if err != nil {
			return nil, "", false, err
		}
		user, err := decodeUserValue(uid, value)
		if err != nil {
			return nil, "", false, err
		}
		users = append(users, user)
		if len(users) == limit {
			return users, uid, false, nil
		}
	}
	if err := iter.Error(); err != nil {
		return nil, "", false, err
	}
	return users, "", true, nil
}

func encodeUserValue(user User) []byte {
	value := appendValueString(nil, user.Token)
	value = appendValueInt64(value, user.DeviceFlag)
	value = appendValueInt64(value, user.DeviceLevel)
	return value
}

func decodeUserValue(uid string, value []byte) (User, error) {
	token, rest, err := readValueString(value)
	if err != nil {
		return User{}, err
	}
	deviceFlag, rest, err := readValueInt64(rest)
	if err != nil {
		return User{}, err
	}
	deviceLevel, rest, err := readValueInt64(rest)
	if err != nil {
		return User{}, err
	}
	if len(rest) != 0 {
		return User{}, dberrors.ErrCorruptValue
	}
	return User{UID: uid, Token: token, DeviceFlag: deviceFlag, DeviceLevel: deviceLevel}, nil
}

func decodeUserKeyUID(prefix []byte, key []byte) (string, bool) {
	if !bytes.HasPrefix(key, prefix) {
		return "", false
	}
	uid, rest, err := keycodec.ReadString(key[len(prefix):])
	if err != nil || len(rest) != 2 {
		return "", false
	}
	return uid, true
}
