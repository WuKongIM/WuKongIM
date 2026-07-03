package cluster

import (
	"context"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestUserMetadataStoreAdaptsNodeFacade(t *testing.T) {
	node := &recordingUserMetadataNode{}
	store := NewUserMetadataStore(node)

	if err := store.CreateUser(context.Background(), metadb.User{UID: "u1"}); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if err := store.UpsertDevice(context.Background(), metadb.Device{UID: "u1", DeviceFlag: 1, Token: "token-1"}); err != nil {
		t.Fatalf("UpsertDevice() error = %v", err)
	}
	if _, err := store.GetUser(context.Background(), "u1"); err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if _, err := store.GetDevice(context.Background(), "u1", 1); err != nil {
		t.Fatalf("GetDevice() error = %v", err)
	}

	if len(node.createdUsers) != 1 || node.createdUsers[0].UID != "u1" {
		t.Fatalf("createdUsers = %#v, want u1", node.createdUsers)
	}
	if len(node.upsertedDevices) != 1 || node.upsertedDevices[0].Token != "token-1" {
		t.Fatalf("upsertedDevices = %#v, want token-1", node.upsertedDevices)
	}
	if node.getUserUID != "u1" || node.getDeviceUID != "u1" || node.getDeviceFlag != 1 {
		t.Fatalf("get calls = user %q device %q/%d, want u1 and u1/1", node.getUserUID, node.getDeviceUID, node.getDeviceFlag)
	}
}

type recordingUserMetadataNode struct {
	createdUsers    []metadb.User
	upsertedDevices []metadb.Device
	getUserUID      string
	getDeviceUID    string
	getDeviceFlag   int64
}

func (n *recordingUserMetadataNode) CreateUserMetadata(_ context.Context, user metadb.User) error {
	n.createdUsers = append(n.createdUsers, user)
	return nil
}

func (n *recordingUserMetadataNode) GetUserMetadata(_ context.Context, uid string) (metadb.User, error) {
	n.getUserUID = uid
	return metadb.User{UID: uid}, nil
}

func (n *recordingUserMetadataNode) UpsertDeviceMetadata(_ context.Context, device metadb.Device) error {
	n.upsertedDevices = append(n.upsertedDevices, device)
	return nil
}

func (n *recordingUserMetadataNode) GetDeviceMetadata(_ context.Context, uid string, deviceFlag int64) (metadb.Device, error) {
	n.getDeviceUID = uid
	n.getDeviceFlag = deviceFlag
	return metadb.Device{UID: uid, DeviceFlag: deviceFlag}, nil
}
