package sim

import (
	"reflect"
	"testing"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestBuildPlanCreatesDeterministicUsersAndGroups(t *testing.T) {
	cfg, err := normalizeConfig(Config{
		Servers:       []string{"http://127.0.0.1:5001"},
		Users:         5,
		Groups:        3,
		GroupMembers:  3,
		RunID:         "run-1",
		UIDPrefix:     "u",
		DevicePrefix:  "d",
		ChannelPrefix: "g",
	})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}

	plan := buildPlan(cfg)

	if len(plan.Users) != 5 {
		t.Fatalf("users = %d, want 5", len(plan.Users))
	}
	if len(plan.Groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(plan.Groups))
	}
	if plan.Users[0].UID != "u-000001" || plan.Users[0].DeviceID != "d-000001" {
		t.Fatalf("first user = %#v", plan.Users[0])
	}
	if plan.Groups[0].ChannelID != "g-000001" {
		t.Fatalf("first group channel = %q", plan.Groups[0].ChannelID)
	}
	if !reflect.DeepEqual(plan.Groups[0].Subscribers, []string{"u-000001", "u-000002", "u-000003"}) {
		t.Fatalf("group 0 subscribers = %#v", plan.Groups[0].Subscribers)
	}
	if !reflect.DeepEqual(plan.Groups[1].Subscribers, []string{"u-000004", "u-000005", "u-000001"}) {
		t.Fatalf("group 1 subscribers = %#v", plan.Groups[1].Subscribers)
	}
	if !reflect.DeepEqual(identitiesFromPlan(plan), []wkclient.Identity{
		{UID: "u-000001", DeviceID: "d-000001"},
		{UID: "u-000002", DeviceID: "d-000002"},
		{UID: "u-000003", DeviceID: "d-000003"},
		{UID: "u-000004", DeviceID: "d-000004"},
		{UID: "u-000005", DeviceID: "d-000005"},
	}) {
		t.Fatalf("identities = %#v", identitiesFromPlan(plan))
	}
}

func TestGroupNextMessageBuildsDeterministicRoutedMessage(t *testing.T) {
	cfg, err := normalizeConfig(Config{
		Servers:       []string{"http://127.0.0.1:5001"},
		Users:         2,
		Groups:        1,
		GroupMembers:  2,
		RunID:         "run-1",
		UIDPrefix:     "u",
		DevicePrefix:  "d",
		ChannelPrefix: "g",
		PayloadSize:   "4B",
	})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	plan := buildPlan(cfg)

	got := plan.Groups[0].nextMessage(cfg, 7)

	if got.UID != "u-000001" {
		t.Fatalf("UID = %q, want u-000001", got.UID)
	}
	if got.Message.ChannelID != "g-000001" {
		t.Fatalf("ChannelID = %q, want g-000001", got.Message.ChannelID)
	}
	if got.Message.ChannelType != frame.ChannelTypeGroup {
		t.Fatalf("ChannelType = %d, want %d", got.Message.ChannelType, frame.ChannelTypeGroup)
	}
	if got.Message.ClientSeq != 7 {
		t.Fatalf("ClientSeq = %d, want 7", got.Message.ClientSeq)
	}
	if got.Message.ClientMsgNo != "run-1-g-000001-u-000001-000000000007" {
		t.Fatalf("ClientMsgNo = %q", got.Message.ClientMsgNo)
	}
	if string(got.Message.Payload) != "ssss" {
		t.Fatalf("Payload = %q, want ssss", string(got.Message.Payload))
	}
}
