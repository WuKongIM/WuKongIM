package message

import (
	"context"
	"testing"
)

func TestChannelLogGetLastVisibleMessageReadsTail(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	log := store.db.Channel(ChannelKey("tail:2"), ChannelID{ID: "tail", Type: 2})
	ctx := context.Background()

	_, err := log.Append(ctx, []Record{
		{ID: 101, FromUID: "u1", ClientMsgNo: "c1", Payload: []byte("one"), ServerTimestampMS: 1000},
		{ID: 102, FromUID: "u2", ClientMsgNo: "c2", Payload: []byte("two"), ServerTimestampMS: 2000},
	}, AppendOptions{})
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	msg, ok, err := log.GetLastVisibleMessage(ctx, 0)
	if err != nil || !ok {
		t.Fatalf("GetLastVisibleMessage() ok=%v err=%v, want ok", ok, err)
	}
	if msg.MessageID != 102 || msg.MessageSeq != 2 || msg.FromUID != "u2" || msg.ClientMsgNo != "c2" || msg.ServerTimestampMS != 2000 || string(msg.Payload) != "two" {
		t.Fatalf("last message = %+v, want seq 2 with durable fields", msg)
	}
}

func TestChannelLogGetLastVisibleMessageHonorsVisibleAfterSeq(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	log := store.db.Channel(ChannelKey("visible:2"), ChannelID{ID: "visible", Type: 2})
	ctx := context.Background()

	_, err := log.Append(ctx, []Record{
		{ID: 201, Payload: []byte("one"), ServerTimestampMS: 1000},
		{ID: 202, Payload: []byte("two"), ServerTimestampMS: 2000},
	}, AppendOptions{})
	if err != nil {
		t.Fatalf("Append(): %v", err)
	}

	msg, ok, err := log.GetLastVisibleMessage(ctx, 1)
	if err != nil || !ok {
		t.Fatalf("GetLastVisibleMessage(1) ok=%v err=%v, want ok", ok, err)
	}
	if msg.MessageSeq != 2 {
		t.Fatalf("visible last seq = %d, want 2", msg.MessageSeq)
	}
	_, ok, err = log.GetLastVisibleMessage(ctx, 2)
	if err != nil {
		t.Fatalf("GetLastVisibleMessage(2): %v", err)
	}
	if ok {
		t.Fatal("GetLastVisibleMessage(2) ok = true, want no visible message")
	}
}

func TestChannelLogGetLastVisibleMessageEmptyChannel(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)
	log := store.db.Channel(ChannelKey("empty:2"), ChannelID{ID: "empty", Type: 2})

	_, ok, err := log.GetLastVisibleMessage(context.Background(), 0)
	if err != nil {
		t.Fatalf("GetLastVisibleMessage(empty): %v", err)
	}
	if ok {
		t.Fatal("GetLastVisibleMessage(empty) ok = true, want no visible message")
	}
}
