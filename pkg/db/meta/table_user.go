package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

// User stores token defaults for a UID.
type User struct {
	UID         string
	Token       string
	DeviceFlag  int64
	DeviceLevel int64
}

var userTable = registerMetaTable(TableSpec[User]{
	ID:   TableIDUser,
	Name: "user",
	Columns: []schema.Column{
		{ID: columnIDStringKey, Name: "key", Type: schema.TypeString, Required: true},
		{ID: columnIDValue, Name: "value", Type: schema.TypeBytes},
	},
	Families: []schema.Family{{ID: userPrimaryFamilyID, Name: "primary", Columns: []uint16{columnIDValue}}},
	Primary: PrimarySpec[User]{
		IndexID:  userPrimaryIndexID,
		FamilyID: userPrimaryFamilyID,
		Name:     "pk_user",
		Columns:  []uint16{columnIDStringKey},
		Layout:   KeyLayout{KeyString},
		Key:      func(user User) KeyParts { return KeyParts{String(user.UID)} },
	},
	Validate: func(user User) error { return validateKeyString(user.UID) },
	EncodeValue: func(user User) ([]byte, error) {
		return encodeUserValue(user), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (User, error) {
		return decodeUserValue(primary[0].S, value)
	},
})

// UserTable describes the user table schema.
var UserTable = userTable.Schema()

// CreateUser inserts a user and rejects duplicates.
func (s *Shard) CreateUser(ctx context.Context, user User) error {
	return userTable.Create(ctx, s, user)
}

// UpsertUser stores a user regardless of prior existence.
func (s *Shard) UpsertUser(ctx context.Context, user User) error {
	return userTable.Upsert(ctx, s, user)
}

// UpdateUser updates an existing user.
func (s *Shard) UpdateUser(ctx context.Context, user User) error {
	return userTable.Update(ctx, s, user)
}

// GetUser returns one user by UID.
func (s *Shard) GetUser(ctx context.Context, uid string) (User, bool, error) {
	if err := validateKeyString(uid); err != nil {
		return User{}, false, err
	}
	return userTable.Get(ctx, s, KeyParts{String(uid)})
}

// DeleteUser removes one user by UID.
func (s *Shard) DeleteUser(ctx context.Context, uid string) error {
	if err := validateKeyString(uid); err != nil {
		return err
	}
	return userTable.Delete(ctx, s, KeyParts{String(uid)})
}

// ListUsersPage returns users after cursorUID in ascending UID order.
func (s *Shard) ListUsersPage(ctx context.Context, cursorUID string, limit int) ([]User, string, bool, error) {
	if limit <= 0 {
		return nil, "", true, nil
	}
	var after KeyParts
	if cursorUID != "" {
		if err := validateKeyString(cursorUID); err != nil {
			return nil, "", false, err
		}
		after = KeyParts{String(cursorUID)}
	}
	users, cursor, done, err := userTable.ScanPrimary(ctx, s, after, limit)
	if err != nil || done || len(cursor) == 0 {
		return users, "", done, err
	}
	return users, cursor[0].S, done, nil
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
