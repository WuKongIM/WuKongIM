package user

import (
	"errors"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type UpdateTokenCommand struct {
	UID         string
	Token       string
	DeviceFlag  frame.DeviceFlag
	DeviceLevel frame.DeviceLevel
}

// DeviceQuitCommand requests that a user's device token be cleared.
type DeviceQuitCommand struct {
	UID        string
	DeviceFlag int
}

// OnlineStatus describes one device-level online entry returned by the legacy API.
type OnlineStatus struct {
	UID        string `json:"uid"`
	DeviceFlag uint8  `json:"device_flag"`
	Online     int    `json:"online"`
}

func (c UpdateTokenCommand) Validate() error {
	switch {
	case c.UID == "":
		return errors.New("uid不能为空！")
	case c.Token == "":
		return errors.New("token不能为空！")
	case strings.Contains(c.UID, "@"), strings.Contains(c.UID, "#"), strings.Contains(c.UID, "&"):
		return errors.New("uid不能包含特殊字符！")
	default:
		return nil
	}
}
