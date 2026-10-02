package message

import (
	"context"
	"strings"
	"unicode/utf8"
)

// PublishPermissionQuery names an authenticated sender and an ordinary IM
// destination. Person TargetID is a peer UID, never an encoded conversation.
// No device identity or privileged SEND controls can cross this boundary.
type PublishPermissionQuery struct {
	FromUID    string
	TargetID   string
	TargetType uint8
}

// CheckPublishPermission reads current publish policy without submitting a
// message, creating a directory or running payload hooks. It grants no future
// publication: delayed work must check again when it actually executes.
func (a *App) CheckPublishPermission(ctx context.Context, q PublishPermissionQuery) (Reason, error) {
	if ctx == nil || !validPublishIdentity(q.FromUID) || !validPublishIdentity(q.TargetID) || (q.TargetType != channelTypePerson && q.TargetType != channelTypeGroup) || q.TargetType == channelTypePerson && (strings.Contains(q.FromUID, "@") || strings.Contains(q.TargetID, "@")) {
		return ReasonInvalidRequest, ErrInvalidCommand
	}
	if err := ctx.Err(); err != nil {
		return ReasonSystemError, err
	}
	if a == nil || a.permissionAuthority == nil {
		return ReasonSystemError, ErrRouteNotReady
	}
	if a.commandChannels.IsCommandChannel(q.TargetID) {
		return ReasonInvalidRequest, ErrInvalidCommand
	}
	// App contains immutable dependency references, not synchronization state.
	// The request-local copy retains the shared policy while bypassing only its
	// optional SEND cache; never mutate the live App or its cache in place.
	policy := *a
	policy.permissions = a.permissionAuthority
	_, reason, err := policy.checkSendPermission(ctx, SendCommand{
		FromUID: q.FromUID, ChannelID: q.TargetID, ChannelType: q.TargetType,
		NormalizePersonChannel: q.TargetType == channelTypePerson,
	})
	return reason, err
}

func validPublishIdentity(s string) bool {
	return len(s) <= 1024 && strings.TrimSpace(s) != "" && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
