package sim

import (
	"bytes"
	"fmt"
	"sync/atomic"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// Plan describes the deterministic users and groups for one simulation run.
type Plan struct {
	// Users are generated in stable UID order.
	Users []User
	// Groups are generated in stable channel order.
	Groups []Group
}

// User describes one simulated client identity.
type User struct {
	// UID is the generated WuKong user id.
	UID string
	// DeviceID is the generated client device id.
	DeviceID string
}

// Group describes one simulated group channel and its subscribers.
type Group struct {
	// ChannelID is the generated group channel id.
	ChannelID string
	// Subscribers are the generated user ids assigned to the group.
	Subscribers []string

	next atomic.Uint64
}

// buildPlan creates deterministic users and group memberships from normalized config.
func buildPlan(cfg Config) Plan {
	plan := Plan{
		Users:  make([]User, 0, cfg.Users),
		Groups: make([]Group, cfg.Groups),
	}
	for i := 1; i <= cfg.Users; i++ {
		plan.Users = append(plan.Users, User{
			UID:      fmt.Sprintf("%s-%06d", cfg.UIDPrefix, i),
			DeviceID: fmt.Sprintf("%s-%06d", cfg.DevicePrefix, i),
		})
	}
	for i := 0; i < cfg.Groups; i++ {
		group := &plan.Groups[i]
		group.ChannelID = fmt.Sprintf("%s-%06d", cfg.ChannelPrefix, i+1)
		group.Subscribers = make([]string, 0, cfg.GroupMembers)
		for j := 0; j < cfg.GroupMembers; j++ {
			userIndex := (i*cfg.GroupMembers + j) % len(plan.Users)
			group.Subscribers = append(group.Subscribers, plan.Users[userIndex].UID)
		}
	}
	return plan
}

// identitiesFromPlan converts generated users into client pool identities.
func identitiesFromPlan(plan Plan) []wkclient.Identity {
	identities := make([]wkclient.Identity, 0, len(plan.Users))
	for _, user := range plan.Users {
		identities = append(identities, wkclient.Identity{
			UID:      user.UID,
			DeviceID: user.DeviceID,
		})
	}
	return identities
}

// nextMessage builds the next deterministic routed SEND message for the group.
func (g *Group) nextMessage(cfg Config, sequence uint64) wkclient.RoutedMessage {
	sender := g.Subscribers[int(g.next.Add(1)-1)%len(g.Subscribers)]
	payload := bytes.Repeat([]byte{'s'}, cfg.PayloadBytes)
	return wkclient.RoutedMessage{
		UID: sender,
		Message: wkclient.Message{
			ClientSeq:   sequence,
			ClientMsgNo: fmt.Sprintf("%s-%s-%s-%012d", cfg.RunID, g.ChannelID, sender, sequence),
			ChannelID:   g.ChannelID,
			ChannelType: frame.ChannelTypeGroup,
			Payload:     payload,
		},
	}
}
