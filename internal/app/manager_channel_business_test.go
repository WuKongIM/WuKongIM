package app

import (
	"context"
	"errors"
	"strconv"
	"testing"

	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestNewManagerManagementWiresBusinessChannelOperatorToChannelUsecase(t *testing.T) {
	store := &managerChannelBusinessStore{channels: map[string]metadb.Channel{
		"g1:2": {ChannelID: "g1", ChannelType: 2, Ban: 1},
	}}
	app := &App{
		cluster:  managerChannelBusinessWiringCluster{},
		channels: channelusecase.New(channelusecase.Options{Store: store}),
	}
	management := app.newManagerManagement()
	if management == nil {
		t.Fatal("newManagerManagement() = nil")
	}

	detail, err := management.GetBusinessChannel(context.Background(), "g1", 2)
	if err != nil || !detail.Ban {
		t.Fatalf("GetBusinessChannel() = %#v err=%v", detail, err)
	}
	detail, err = management.UpdateBusinessChannel(context.Background(), managementusecase.UpdateBusinessChannelRequest{
		ChannelID: "g1", ChannelType: 2, SendBan: true,
	})
	if err != nil {
		t.Fatalf("UpdateBusinessChannel(): %v", err)
	}
	if detail.Ban || !detail.SendBan {
		t.Fatalf("patched detail = %#v", detail)
	}
}

type managerChannelBusinessWiringCluster struct{}

func (managerChannelBusinessWiringCluster) Start(context.Context) error { return nil }
func (managerChannelBusinessWiringCluster) Stop(context.Context) error  { return nil }
func (managerChannelBusinessWiringCluster) NodeID() uint64              { return 1 }
func (managerChannelBusinessWiringCluster) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return control.Snapshot{
		HashSlots: control.HashSlotTable{
			Count:  4,
			Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}},
		},
		Slots: []control.SlotAssignment{{SlotID: 1}},
	}, nil
}

type managerChannelBusinessStore struct {
	channels map[string]metadb.Channel
}

func (s *managerChannelBusinessStore) GetChannel(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	channel, ok := s.channels[managerChannelBusinessStoreKey(channelID, channelType)]
	if !ok {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return channel, nil
}

func (s *managerChannelBusinessStore) UpsertChannel(_ context.Context, channel metadb.Channel) error {
	s.channels[managerChannelBusinessStoreKey(channel.ChannelID, channel.ChannelType)] = channel
	return nil
}

func (s *managerChannelBusinessStore) CreateChannelStrict(ctx context.Context, channel metadb.Channel) error {
	if _, err := s.GetChannel(ctx, channel.ChannelID, channel.ChannelType); err == nil {
		return metadb.ErrAlreadyExists
	} else if !errors.Is(err, metadb.ErrNotFound) {
		return err
	}
	return s.UpsertChannel(ctx, channel)
}

func (s *managerChannelBusinessStore) PatchChannelBusinessFlags(ctx context.Context, channelID string, channelType int64, flags metadb.ChannelBusinessFlags) error {
	channel, err := s.GetChannel(ctx, channelID, channelType)
	if err != nil {
		return err
	}
	channel.Ban = flags.Ban
	channel.Disband = flags.Disband
	channel.SendBan = flags.SendBan
	return s.UpsertChannel(ctx, channel)
}

func (*managerChannelBusinessStore) DeleteChannel(context.Context, string, int64) error {
	return nil
}

func (*managerChannelBusinessStore) AddChannelSubscribers(context.Context, string, int64, []string, ...uint64) error {
	return nil
}

func (*managerChannelBusinessStore) RemoveChannelSubscribers(context.Context, string, int64, []string, ...uint64) error {
	return nil
}

func (*managerChannelBusinessStore) ListChannelSubscribers(context.Context, string, int64, string, int) ([]string, string, bool, error) {
	return nil, "", true, nil
}

func (*managerChannelBusinessStore) ContainsChannelSubscriber(context.Context, string, int64, string) (bool, error) {
	return false, nil
}

func (*managerChannelBusinessStore) HasChannelSubscribers(context.Context, string, int64) (bool, error) {
	return false, nil
}

func managerChannelBusinessStoreKey(channelID string, channelType int64) string {
	return channelID + ":" + strconv.FormatInt(channelType, 10)
}
