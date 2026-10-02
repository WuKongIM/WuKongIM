// Package mqtt maps MQTT application conventions to entry-neutral IM identities.
// Authoritative credentials, membership and session policy remain in use cases.
package mqtt

import (
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/internal/contracts/protocolmeta"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
)

const (
	maxIdentityBytes      = 1024
	maxTokenBytes         = 16 << 10
	deviceFlagProperty    = "wk.device_flag"
	clientMessageProperty = "wk.client_msg_no"
)

// Target is the client-visible IM destination; person Channel normalization
// still belongs to the existing message use case, using the authenticated UID.
type Target struct {
	ChannelID   string
	ChannelType uint8
}

// ParseTopic accepts one canonical exact application topic. Decoded IDs may
// contain MQTT separators because base64url keeps them within one topic level.
func ParseTopic(topic string) (Target, error) {
	var target Target
	if len(topic) > 2*maxIdentityBytes {
		return target, invalidTopic()
	}
	parts := strings.Split(topic, "/")
	if len(parts) != 5 || parts[0] != "wk" || parts[1] != "v1" || parts[4] != "messages" {
		return target, invalidTopic()
	}
	switch parts[2] {
	case "users":
		target.ChannelType = uint8(protocolmeta.ChannelTypePerson)
	case "groups":
		target.ChannelType = 2
	default:
		return Target{}, invalidTopic()
	}
	id, err := base64.RawURLEncoding.Strict().DecodeString(parts[3])
	if err != nil || !validIdentity(string(id)) || base64.RawURLEncoding.EncodeToString(id) != parts[3] {
		return Target{}, invalidTopic()
	}
	target.ChannelID = string(id)
	return target, nil
}

// FormatTopic is the inverse canonical encoding used for outbound publications.
func FormatTopic(target Target) (string, error) {
	if !validIdentity(target.ChannelID) {
		return "", invalidTopic()
	}
	var kind string
	switch target.ChannelType {
	case 1:
		kind = "users"
	case 2:
		kind = "groups"
	default:
		return "", invalidTopic()
	}
	return "wk/v1/" + kind + "/" + base64.RawURLEncoding.EncodeToString([]byte(target.ChannelID)) + "/messages", nil
}

// Identity is unverified CONNECT input. The token verifier must succeed before
// this identity can acquire a session or invoke any IM operation.
type Identity struct {
	UID        string
	ClientID   string
	DeviceFlag protocolmeta.DeviceFlag
	Token      string
}

// Credentials validates the application handshake without changing credential
// category or device level. MQTT ClientID ownership is acquired after verification.
func Credentials(c *wire.Connect) (Identity, error) {
	var identity Identity
	if c == nil || !validIdentity(c.ClientID) {
		return identity, &wire.Error{Reason: 0x85, Detail: "invalid client identifier"}
	}
	if !c.UsernameFlag || !validIdentity(c.Username) || !c.PasswordFlag || len(c.Password) == 0 || len(c.Password) > maxTokenBytes {
		return identity, &wire.Error{Reason: 0x86, Detail: "invalid credential fields"}
	}
	for _, p := range c.Properties {
		if p.ID == wire.AuthenticationMethod || p.ID == wire.AuthenticationData {
			return identity, &wire.Error{Reason: 0x8c, Detail: "enhanced authentication not supported"}
		}
	}
	flag, err := reservedProperty(c.Properties, deviceFlagProperty, 0x86)
	if err != nil {
		return identity, err
	}
	switch flag {
	case "0":
		identity.DeviceFlag = protocolmeta.DeviceFlagApp
	case "1":
		identity.DeviceFlag = protocolmeta.DeviceFlagWeb
	case "2":
		identity.DeviceFlag = protocolmeta.DeviceFlagPC
	default:
		return Identity{}, &wire.Error{Reason: 0x86, Detail: "invalid credential category"}
	}
	identity.UID, identity.ClientID, identity.Token = c.Username, c.ClientID, string(c.Password)
	return identity, nil
}

// ClientMessageNumber extracts a stable application retry key. It does not
// derive idempotency from a 16-bit Packet Identifier or the current connection.
func ClientMessageNumber(properties []wire.Property) (string, error) {
	return reservedProperty(properties, clientMessageProperty, 0x83)
}

func reservedProperty(properties []wire.Property, name string, invalidReason byte) (string, error) {
	var value string
	found := false
	for _, p := range properties {
		if p.ID != wire.UserProperty || !strings.HasPrefix(p.Text, "wk.") {
			continue
		}
		if p.Text != name {
			return "", &wire.Error{Reason: 0x87, Detail: "reserved application property"}
		}
		if found || !validIdentity(p.Value) {
			return "", &wire.Error{Reason: invalidReason, Detail: "invalid application property"}
		}
		value, found = p.Value, true
	}
	if !found {
		return "", &wire.Error{Reason: invalidReason, Detail: "required application property missing"}
	}
	return value, nil
}

func validIdentity(s string) bool {
	return len(s) <= maxIdentityBytes && strings.TrimSpace(s) != "" && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func invalidTopic() error { return &wire.Error{Reason: 0x90, Detail: "invalid application topic"} }
