package meta

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOpenClose(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
}

func TestErrorsExposeStableSentinels(t *testing.T) {
	if !errors.Is(ErrNotFound, ErrNotFound) {
		t.Fatal("ErrNotFound should match itself")
	}
	if !errors.Is(ErrAlreadyExists, ErrAlreadyExists) {
		t.Fatal("ErrAlreadyExists should match itself")
	}
}

func TestUserCRUDIsSlotScoped(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	left := db.ForSlot(1)
	right := db.ForSlot(2)

	_, err := left.GetUser(ctx, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing user, got %v", err)
	}

	created := User{
		UID:         "u1001",
		Token:       "tk_abc",
		DeviceFlag:  1,
		DeviceLevel: 2,
	}
	if err := left.CreateUser(ctx, created); err != nil {
		t.Fatalf("create user: %v", err)
	}

	if err := left.CreateUser(ctx, created); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}

	_, err = right.GetUser(ctx, created.UID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound in other slot, got %v", err)
	}

	got, err := left.GetUser(ctx, created.UID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !reflect.DeepEqual(got, created) {
		t.Fatalf("unexpected user:\n got: %#v\nwant: %#v", got, created)
	}

	updated := User{
		UID:         "u1001",
		Token:       "tk_new",
		DeviceFlag:  5,
		DeviceLevel: 9,
	}
	if err := left.UpdateUser(ctx, updated); err != nil {
		t.Fatalf("update user: %v", err)
	}

	got, err = left.GetUser(ctx, updated.UID)
	if err != nil {
		t.Fatalf("get updated user: %v", err)
	}
	if !reflect.DeepEqual(got, updated) {
		t.Fatalf("unexpected updated user:\n got: %#v\nwant: %#v", got, updated)
	}

	if err := right.CreateUser(ctx, User{UID: created.UID, Token: "tk_right"}); err != nil {
		t.Fatalf("create same uid in right slot: %v", err)
	}

	if err := left.DeleteUser(ctx, updated.UID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	_, err = left.GetUser(ctx, updated.UID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}

	got, err = right.GetUser(ctx, created.UID)
	if err != nil {
		t.Fatalf("get user from right slot: %v", err)
	}
	if got.Token != "tk_right" {
		t.Fatalf("right slot token = %q", got.Token)
	}
}

func TestCreateUserSameUIDAllowedAcrossSlots(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	left := db.ForSlot(1)
	right := db.ForSlot(2)

	if err := left.CreateUser(ctx, User{UID: "u1001", Token: "left"}); err != nil {
		t.Fatalf("left create: %v", err)
	}
	if err := right.CreateUser(ctx, User{UID: "u1001", Token: "right"}); err != nil {
		t.Fatalf("right create: %v", err)
	}

	leftUser, err := left.GetUser(ctx, "u1001")
	if err != nil {
		t.Fatalf("left get: %v", err)
	}
	rightUser, err := right.GetUser(ctx, "u1001")
	if err != nil {
		t.Fatalf("right get: %v", err)
	}

	if leftUser.Token != "left" || rightUser.Token != "right" {
		t.Fatalf("tokens = (%q, %q)", leftUser.Token, rightUser.Token)
	}
}

func TestCreateUserConcurrentDuplicateReturnsErrAlreadyExists(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(1)

	blocked := make(chan struct{})
	reached := make(chan struct{})
	db.testHooks.afterExistenceCheck = func() {
		close(reached)
		<-blocked
	}

	firstErr := make(chan error, 1)
	secondErr := make(chan error, 1)

	go func() {
		firstErr <- shard.CreateUser(ctx, User{UID: "u2001"})
	}()

	<-reached

	go func() {
		secondErr <- shard.CreateUser(ctx, User{UID: "u2001"})
	}()

	select {
	case err := <-secondErr:
		t.Fatalf("second create returned before first commit: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(blocked)

	if err := <-firstErr; err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := <-secondErr; !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestCreateUserRejectsOverlongUID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(1)

	u := User{UID: strings.Repeat("a", maxKeyStringLen+1)}
	err := shard.CreateUser(ctx, u)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

func TestGetUserHonorsCanceledContext(t *testing.T) {
	db := openTestDB(t)
	shard := db.ForSlot(1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := shard.GetUser(ctx, "u1001")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
