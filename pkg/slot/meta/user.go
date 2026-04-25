package meta

import (
	"context"

	"github.com/cockroachdb/pebble/v2"
)

type User struct {
	UID         string
	Token       string
	DeviceFlag  int64
	DeviceLevel int64
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
