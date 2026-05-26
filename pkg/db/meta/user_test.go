package meta

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestUserCRUDAndPage(t *testing.T) {
	store := openTestMetaStore(t)
	defer store.close(t)
	shard := store.db.HashSlot(1)

	if _, ok, err := shard.GetUser(context.Background(), "missing"); err != nil || ok {
		t.Fatalf("GetUser(missing) = ok %v err %v, want missing", ok, err)
	}
	user := User{UID: "u1", Token: "tk1", DeviceFlag: 1, DeviceLevel: 2}
	if err := shard.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	if err := shard.CreateUser(context.Background(), user); !errors.Is(err, dberrors.ErrAlreadyExists) {
		t.Fatalf("CreateUser() duplicate err = %v, want already exists", err)
	}
	got, ok, err := shard.GetUser(context.Background(), "u1")
	if err != nil || !ok || got != user {
		t.Fatalf("GetUser() = (%+v, %v, %v), want %+v", got, ok, err, user)
	}
	updated := User{UID: "u1", Token: "tk2", DeviceFlag: 5, DeviceLevel: 9}
	if err := shard.UpdateUser(context.Background(), updated); err != nil {
		t.Fatalf("UpdateUser(): %v", err)
	}
	if err := shard.UpdateUser(context.Background(), User{UID: "missing"}); !errors.Is(err, dberrors.ErrNotFound) {
		t.Fatalf("UpdateUser(missing) err = %v, want not found", err)
	}
	if err := shard.UpsertUser(context.Background(), User{UID: "u2", Token: "tk3"}); err != nil {
		t.Fatalf("UpsertUser(): %v", err)
	}
	page, cursor, done, err := shard.ListUsersPage(context.Background(), "", 1)
	if err != nil {
		t.Fatalf("ListUsersPage(): %v", err)
	}
	if done || cursor != "u1" || len(page) != 1 || page[0].UID != "u1" {
		t.Fatalf("first page = %+v cursor=%q done=%v", page, cursor, done)
	}
	page, cursor, done, err = shard.ListUsersPage(context.Background(), cursor, 2)
	if err != nil {
		t.Fatalf("ListUsersPage(next): %v", err)
	}
	if !done || cursor != "" || len(page) != 1 || page[0].UID != "u2" {
		t.Fatalf("next page = %+v cursor=%q done=%v", page, cursor, done)
	}
	if err := shard.DeleteUser(context.Background(), "u1"); err != nil {
		t.Fatalf("DeleteUser(): %v", err)
	}
	if _, ok, err := shard.GetUser(context.Background(), "u1"); err != nil || ok {
		t.Fatalf("GetUser(deleted) = ok %v err %v, want missing", ok, err)
	}
}

func TestUserTableRuntimeDescriptor(t *testing.T) {
	if userTable.Schema().ID != TableIDUser {
		t.Fatalf("user table id = %d, want %d", userTable.Schema().ID, TableIDUser)
	}
	if userTable.Schema().Primary.Name != "pk_user" {
		t.Fatalf("user primary name = %q", userTable.Schema().Primary.Name)
	}
}
