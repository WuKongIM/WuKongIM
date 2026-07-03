package management

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"hash/crc32"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	userusecase "github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const defaultUserListInternalScanLimit = 200

// UserReader exposes durable user and device reads for manager pages.
type UserReader interface {
	// ScanUsersSlotPage returns one user metadata page for a physical Slot.
	ScanUsersSlotPage(ctx context.Context, slotID uint32, after metadb.UserCursor, limit int) ([]metadb.User, metadb.UserCursor, bool, error)
	// GetUser returns one durable user metadata row.
	GetUser(ctx context.Context, uid string) (metadb.User, error)
	// GetDevice returns one durable device token row.
	GetDevice(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error)
}

// UserOperator exposes user mutations reused by manager actions.
type UserOperator interface {
	// UpdateToken creates or replaces a user's device token.
	UpdateToken(ctx context.Context, cmd userusecase.UpdateTokenCommand) error
	// DeviceQuit clears stored device tokens and kicks matching local sessions.
	DeviceQuit(ctx context.Context, cmd userusecase.DeviceQuitCommand) error
}

// UserPresenceDirectory exposes authoritative online routes keyed by UID.
type UserPresenceDirectory interface {
	// EndpointsByUIDs returns authoritative online routes keyed by UID.
	EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]presence.Route, error)
}

// UserRouteActionDispatcher applies manager force-offline actions on route owners.
type UserRouteActionDispatcher interface {
	// ApplyRouteAction applies a close/kick action on the route owner node.
	ApplyRouteAction(ctx context.Context, action presence.RouteAction) error
}

// UserListCursor identifies the next manager user list position.
type UserListCursor struct {
	// SlotID is the current physical Slot scan position.
	SlotID uint32
	// UID is the last emitted user ID inside SlotID.
	UID string
	// KeywordHash binds the opaque cursor to the keyword used to create it.
	KeywordHash uint32
}

// ListUsersRequest configures a manager user page request.
type ListUsersRequest struct {
	// Limit is the maximum number of items to return.
	Limit int
	// Cursor resumes a previous user list request.
	Cursor UserListCursor
	// Keyword optionally limits results to UIDs containing this substring.
	Keyword string
}

// ListUsersResponse is the manager user page result.
type ListUsersResponse struct {
	// Items contains the ordered page items.
	Items []UserListItem
	// HasMore reports whether another page exists after this one.
	HasMore bool
	// NextCursor identifies the next page position when HasMore is true.
	NextCursor UserListCursor
}

// UserListItem is the manager-facing user list DTO.
type UserListItem struct {
	// UID is the user identifier.
	UID string
	// SlotID is the physical Slot that owns the user metadata.
	SlotID uint32
	// HashSlot is the logical hash slot derived from the UID.
	HashSlot uint16
	// Online reports whether at least one online route exists.
	Online bool
	// OnlineDeviceCount counts distinct online device flags.
	OnlineDeviceCount int
	// OnlineDeviceFlags lists stable manager-facing online device flags.
	OnlineDeviceFlags []string
	// DeviceCount counts stored device token rows.
	DeviceCount int
	// TokenSetCount counts stored device token rows with non-empty tokens.
	TokenSetCount int
}

// UserDevice is the manager-facing stored-device summary DTO.
type UserDevice struct {
	// DeviceFlag is the stable manager-facing device flag.
	DeviceFlag string
	// DeviceLevel is the stable manager-facing device level.
	DeviceLevel string
	// TokenSet reports whether the stored token is non-empty.
	TokenSet bool
	// Online reports whether at least one route exists for this device flag.
	Online bool
	// OnlineSessionCount counts online sessions for this device flag.
	OnlineSessionCount int
}

// Connection is the manager-facing user connection DTO.
type Connection struct {
	// NodeID identifies the node that owns the real gateway session.
	NodeID uint64
	// SessionID is the owner-local gateway session identifier.
	SessionID uint64
	// UID is the authenticated user identifier.
	UID string
	// DeviceID is the client device identifier.
	DeviceID string
	// DeviceFlag is the stable manager-facing device flag string.
	DeviceFlag string
	// DeviceLevel is the stable manager-facing device level string.
	DeviceLevel string
	// SlotID is reserved for legacy connection response compatibility.
	SlotID uint64
	// State is reserved for legacy connection response compatibility.
	State string
	// Listener is the listener that accepted the connection.
	Listener string
	// ConnectedAt is the owner-observed connection timestamp when known.
	ConnectedAt time.Time
	// RemoteAddr is reserved for owner-local connection details.
	RemoteAddr string
	// LocalAddr is reserved for owner-local connection details.
	LocalAddr string
}

// UserDetail is the manager-facing user detail DTO.
type UserDetail struct {
	// UID is the user identifier.
	UID string
	// SlotID is the physical Slot that owns the user metadata.
	SlotID uint32
	// HashSlot is the logical hash slot derived from the UID.
	HashSlot uint16
	// Online reports whether at least one online route exists.
	Online bool
	// Devices lists stored and online device summaries.
	Devices []UserDevice
	// Connections lists currently known online routes.
	Connections []Connection
}

// KickUserRequest configures a manager force-offline action.
type KickUserRequest struct {
	// UID is the user identifier to kick.
	UID string
	// DeviceFlag is all, app, web, or pc.
	DeviceFlag string
}

// KickUserResponse reports the accepted manager force-offline action.
type KickUserResponse struct {
	// UID is the user identifier.
	UID string
	// DeviceFlag is the normalized device flag label.
	DeviceFlag string
	// Changed reports whether the action was accepted.
	Changed bool
}

// ResetUserTokenRequest configures a manager token reset action.
type ResetUserTokenRequest struct {
	// UID is the user identifier.
	UID string
	// DeviceFlag is app, web, pc, or system.
	DeviceFlag string
	// DeviceLevel is master or slave.
	DeviceLevel string
	// Token optionally supplies the replacement token.
	Token string
}

// ResetUserTokenResponse returns the replacement token once.
type ResetUserTokenResponse struct {
	// UID is the user identifier.
	UID string
	// DeviceFlag is the normalized device flag label.
	DeviceFlag string
	// DeviceLevel is the normalized device level label.
	DeviceLevel string
	// Token is the new token and is only returned by this action.
	Token string
}

// ListUsers returns a manager-facing page ordered by Slot and UID.
func (a *App) ListUsers(ctx context.Context, req ListUsersRequest) (ListUsersResponse, error) {
	if a == nil || a.cluster == nil || a.users == nil {
		return ListUsersResponse{}, nil
	}
	if req.Limit <= 0 {
		return ListUsersResponse{}, metadb.ErrInvalidArgument
	}
	keyword := strings.TrimSpace(req.Keyword)
	if err := validateUserListCursor(req.Cursor, userKeywordHash(keyword)); err != nil {
		return ListUsersResponse{}, err
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return ListUsersResponse{}, err
	}
	slotIDs := sortedSnapshotSlotIDs(snapshot.Slots)
	startIndex, err := channelStartSlotIndex(slotIDs, req.Cursor.SlotID)
	if err != nil {
		return ListUsersResponse{}, err
	}
	if keyword != "" {
		return a.listUsersByKeyword(ctx, snapshot, slotIDs, startIndex, req.Cursor, req.Limit, keyword)
	}
	return a.listUsersUnfiltered(ctx, snapshot, slotIDs, startIndex, req.Cursor, req.Limit, keyword)
}

func (a *App) listUsersUnfiltered(ctx context.Context, snapshot control.Snapshot, slotIDs []uint32, startIndex int, cursor UserListCursor, limit int, keyword string) (ListUsersResponse, error) {
	resp := ListUsersResponse{Items: make([]UserListItem, 0, limit)}
	for i := startIndex; i < len(slotIDs) && len(resp.Items) < limit; i++ {
		slotID := slotIDs[i]
		after := metadb.UserCursor{}
		if i == startIndex {
			after = cursor.shardCursor()
		}
		page, nextCursor, done, err := a.users.ScanUsersSlotPage(ctx, slotID, after, limit-len(resp.Items))
		if err != nil {
			return ListUsersResponse{}, err
		}
		items, err := a.managerUserListItems(ctx, snapshot, slotID, page)
		if err != nil {
			return ListUsersResponse{}, err
		}
		resp.Items = append(resp.Items, items...)
		if !done {
			resp.HasMore = true
			resp.NextCursor = newUserListCursor(slotID, nextCursor, keyword)
			return resp, nil
		}
		if len(resp.Items) == limit {
			hasMore, _, err := a.nextUserSlotWithData(ctx, slotIDs[i+1:])
			if err != nil {
				return ListUsersResponse{}, err
			}
			resp.HasMore = hasMore
			if hasMore {
				resp.NextCursor = userListCursorForItem(resp.Items[len(resp.Items)-1], keyword)
			}
			return resp, nil
		}
	}
	return resp, nil
}

func (a *App) listUsersByKeyword(ctx context.Context, snapshot control.Snapshot, slotIDs []uint32, startIndex int, cursor UserListCursor, limit int, keyword string) (ListUsersResponse, error) {
	resp := ListUsersResponse{Items: make([]UserListItem, 0, limit)}
	for i := startIndex; i < len(slotIDs); i++ {
		slotID := slotIDs[i]
		after := metadb.UserCursor{}
		if i == startIndex {
			after = cursor.shardCursor()
		}
		for {
			page, nextCursor, done, err := a.users.ScanUsersSlotPage(ctx, slotID, after, userFilteredScanLimit(limit))
			if err != nil {
				return ListUsersResponse{}, err
			}
			for _, user := range page {
				if !strings.Contains(user.UID, keyword) {
					continue
				}
				if len(resp.Items) == limit {
					resp.HasMore = true
					resp.NextCursor = userListCursorForItem(resp.Items[len(resp.Items)-1], keyword)
					return resp, nil
				}
				item, err := a.managerUserListItem(ctx, snapshot, slotID, user.UID)
				if err != nil {
					return ListUsersResponse{}, err
				}
				resp.Items = append(resp.Items, item)
			}
			if done || nextCursor == after {
				break
			}
			after = nextCursor
		}
	}
	return resp, nil
}

// GetUser returns one manager-facing authoritative user detail DTO.
func (a *App) GetUser(ctx context.Context, uid string) (UserDetail, error) {
	if a == nil || a.cluster == nil || a.users == nil {
		return UserDetail{}, nil
	}
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return UserDetail{}, metadb.ErrInvalidArgument
	}
	if _, err := a.users.GetUser(ctx, uid); err != nil {
		return UserDetail{}, err
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return UserDetail{}, err
	}
	routesByUID, err := a.userRoutes(ctx, []string{uid})
	if err != nil {
		return UserDetail{}, err
	}
	routes := routesByUID[uid]
	devices, err := a.userDevices(ctx, uid, routes)
	if err != nil {
		return UserDetail{}, err
	}
	hashSlot := userHashSlot(snapshot.HashSlots, uid)
	return UserDetail{
		UID:         uid,
		SlotID:      slotIDForHashSlot(snapshot.HashSlots, hashSlot),
		HashSlot:    hashSlot,
		Online:      len(routes) > 0,
		Devices:     devices,
		Connections: managerConnectionsFromRoutes(routes),
	}, nil
}

// KickUser forces one user's sessions offline.
func (a *App) KickUser(ctx context.Context, req KickUserRequest) (KickUserResponse, error) {
	if a == nil || a.userOperator == nil {
		return KickUserResponse{}, fmt.Errorf("management: user operator not configured")
	}
	uid := strings.TrimSpace(req.UID)
	if uid == "" {
		return KickUserResponse{}, metadb.ErrInvalidArgument
	}
	label, quitFlag, matchFlags, err := parseKickUserDeviceFlag(req.DeviceFlag)
	if err != nil {
		return KickUserResponse{}, err
	}
	if err := a.userOperator.DeviceQuit(ctx, userusecase.DeviceQuitCommand{UID: uid, DeviceFlag: quitFlag}); err != nil {
		return KickUserResponse{}, err
	}
	routesByUID, err := a.userRoutes(ctx, []string{uid})
	if err != nil {
		return KickUserResponse{}, err
	}
	if a.userActions != nil {
		for _, route := range routesByUID[uid] {
			if !matchFlags[frame.DeviceFlag(route.DeviceFlag)] {
				continue
			}
			if err := a.userActions.ApplyRouteAction(ctx, presence.RouteAction{
				UID:         route.UID,
				OwnerNodeID: route.OwnerNodeID,
				OwnerBootID: route.OwnerBootID,
				SessionID:   route.SessionID,
				Kind:        "kick_then_close",
				Reason:      "manager force offline",
			}); err != nil {
				return KickUserResponse{}, err
			}
		}
	}
	return KickUserResponse{UID: uid, DeviceFlag: label, Changed: true}, nil
}

// ResetUserToken resets one user device token.
func (a *App) ResetUserToken(ctx context.Context, req ResetUserTokenRequest) (ResetUserTokenResponse, error) {
	if a == nil || a.userOperator == nil {
		return ResetUserTokenResponse{}, fmt.Errorf("management: user operator not configured")
	}
	uid := strings.TrimSpace(req.UID)
	if uid == "" {
		return ResetUserTokenResponse{}, metadb.ErrInvalidArgument
	}
	flag, flagLabel, err := parseUserDeviceFlag(req.DeviceFlag, true)
	if err != nil {
		return ResetUserTokenResponse{}, err
	}
	level, levelLabel, err := parseUserDeviceLevel(req.DeviceLevel)
	if err != nil {
		return ResetUserTokenResponse{}, err
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		token, err = generateManagerUserToken()
		if err != nil {
			return ResetUserTokenResponse{}, err
		}
	}
	if err := a.userOperator.UpdateToken(ctx, userusecase.UpdateTokenCommand{
		UID: uid, Token: token, DeviceFlag: flag, DeviceLevel: level,
	}); err != nil {
		return ResetUserTokenResponse{}, err
	}
	return ResetUserTokenResponse{UID: uid, DeviceFlag: flagLabel, DeviceLevel: levelLabel, Token: token}, nil
}

func (a *App) managerUserListItems(ctx context.Context, snapshot control.Snapshot, slotID uint32, users []metadb.User) ([]UserListItem, error) {
	uids := make([]string, 0, len(users))
	for _, user := range users {
		uids = append(uids, user.UID)
	}
	routesByUID, err := a.userRoutes(ctx, uids)
	if err != nil {
		return nil, err
	}
	out := make([]UserListItem, 0, len(users))
	for _, user := range users {
		item, err := a.managerUserListItemWithRoutes(ctx, snapshot, slotID, user.UID, routesByUID[user.UID])
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (a *App) managerUserListItem(ctx context.Context, snapshot control.Snapshot, slotID uint32, uid string) (UserListItem, error) {
	routesByUID, err := a.userRoutes(ctx, []string{uid})
	if err != nil {
		return UserListItem{}, err
	}
	return a.managerUserListItemWithRoutes(ctx, snapshot, slotID, uid, routesByUID[uid])
}

func (a *App) managerUserListItemWithRoutes(ctx context.Context, snapshot control.Snapshot, slotID uint32, uid string, routes []presence.Route) (UserListItem, error) {
	deviceCount, tokenSetCount, err := a.userDeviceCounts(ctx, uid)
	if err != nil {
		return UserListItem{}, err
	}
	onlineFlags := onlineDeviceFlagLabels(routes)
	return UserListItem{
		UID:               uid,
		SlotID:            slotID,
		HashSlot:          userHashSlot(snapshot.HashSlots, uid),
		Online:            len(routes) > 0,
		OnlineDeviceCount: len(onlineFlags),
		OnlineDeviceFlags: onlineFlags,
		DeviceCount:       deviceCount,
		TokenSetCount:     tokenSetCount,
	}, nil
}

func (a *App) userDeviceCounts(ctx context.Context, uid string) (int, int, error) {
	deviceCount := 0
	tokenSetCount := 0
	for _, flag := range managerUserDeviceFlags() {
		device, ok, err := a.userDevice(ctx, uid, flag)
		if err != nil {
			return 0, 0, err
		}
		if !ok {
			continue
		}
		deviceCount++
		if device.Token != "" {
			tokenSetCount++
		}
	}
	return deviceCount, tokenSetCount, nil
}

func (a *App) userDevices(ctx context.Context, uid string, routes []presence.Route) ([]UserDevice, error) {
	routeCounts := make(map[frame.DeviceFlag]int)
	routeLevels := make(map[frame.DeviceFlag]frame.DeviceLevel)
	for _, route := range routes {
		flag := frame.DeviceFlag(route.DeviceFlag)
		routeCounts[flag]++
		if _, ok := routeLevels[flag]; !ok {
			routeLevels[flag] = frame.DeviceLevel(route.DeviceLevel)
		}
	}

	out := make([]UserDevice, 0, len(managerUserDeviceFlags()))
	for _, flag := range managerUserDeviceFlags() {
		device, ok, err := a.userDevice(ctx, uid, flag)
		if err != nil {
			return nil, err
		}
		onlineCount := routeCounts[flag]
		if !ok && onlineCount == 0 {
			continue
		}
		level := frame.DeviceLevel(device.DeviceLevel)
		if !ok {
			level = routeLevels[flag]
		}
		out = append(out, UserDevice{
			DeviceFlag:         managerDeviceFlag(flag),
			DeviceLevel:        managerConnectionDeviceLevel(level),
			TokenSet:           ok && device.Token != "",
			Online:             onlineCount > 0,
			OnlineSessionCount: onlineCount,
		})
	}
	return out, nil
}

func (a *App) userDevice(ctx context.Context, uid string, flag frame.DeviceFlag) (metadb.Device, bool, error) {
	if a == nil || a.users == nil {
		return metadb.Device{}, false, nil
	}
	device, err := a.users.GetDevice(ctx, uid, int64(flag))
	if err != nil {
		if err == metadb.ErrNotFound {
			return metadb.Device{}, false, nil
		}
		return metadb.Device{}, false, err
	}
	if device.UID == "" {
		return metadb.Device{}, false, nil
	}
	return device, true, nil
}

func (a *App) userRoutes(ctx context.Context, uids []string) (map[string][]presence.Route, error) {
	if a == nil || a.userPresence == nil || len(uids) == 0 {
		return map[string][]presence.Route{}, nil
	}
	return a.userPresence.EndpointsByUIDs(ctx, uids)
}

func (a *App) nextUserSlotWithData(ctx context.Context, slotIDs []uint32) (bool, uint32, error) {
	for _, slotID := range slotIDs {
		page, _, _, err := a.users.ScanUsersSlotPage(ctx, slotID, metadb.UserCursor{}, 1)
		if err != nil {
			return false, 0, err
		}
		if len(page) > 0 {
			return true, slotID, nil
		}
	}
	return false, 0, nil
}

func managerConnectionsFromRoutes(routes []presence.Route) []Connection {
	out := make([]Connection, 0, len(routes))
	for _, route := range routes {
		out = append(out, Connection{
			NodeID:      route.OwnerNodeID,
			SessionID:   route.SessionID,
			UID:         route.UID,
			DeviceID:    route.DeviceID,
			DeviceFlag:  managerDeviceFlag(frame.DeviceFlag(route.DeviceFlag)),
			DeviceLevel: managerConnectionDeviceLevel(frame.DeviceLevel(route.DeviceLevel)),
			Listener:    route.Listener,
		})
	}
	return out
}

func onlineDeviceFlagLabels(routes []presence.Route) []string {
	seen := make(map[frame.DeviceFlag]struct{})
	for _, route := range routes {
		seen[frame.DeviceFlag(route.DeviceFlag)] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	labels := make([]string, 0, len(seen))
	for _, flag := range managerUserDeviceFlags() {
		if _, ok := seen[flag]; ok {
			labels = append(labels, managerDeviceFlag(flag))
		}
	}
	return labels
}

func managerUserDeviceFlags() []frame.DeviceFlag {
	return []frame.DeviceFlag{frame.APP, frame.WEB, frame.PC, frame.SYSTEM}
}

func parseKickUserDeviceFlag(raw string) (string, int, map[frame.DeviceFlag]bool, error) {
	label := strings.ToLower(strings.TrimSpace(raw))
	if label == "" {
		label = "all"
	}
	if label == "all" {
		return "all", -1, map[frame.DeviceFlag]bool{frame.APP: true, frame.WEB: true, frame.PC: true}, nil
	}
	flag, normalized, err := parseUserDeviceFlag(label, false)
	if err != nil {
		return "", 0, nil, err
	}
	return normalized, int(flag), map[frame.DeviceFlag]bool{flag: true}, nil
}

func parseUserDeviceFlag(raw string, allowSystem bool) (frame.DeviceFlag, string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "app", "":
		return frame.APP, "app", nil
	case "web":
		return frame.WEB, "web", nil
	case "pc":
		return frame.PC, "pc", nil
	case "system":
		if allowSystem {
			return frame.SYSTEM, "system", nil
		}
		return 0, "", metadb.ErrInvalidArgument
	default:
		return 0, "", metadb.ErrInvalidArgument
	}
}

func parseUserDeviceLevel(raw string) (frame.DeviceLevel, string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "master", "":
		return frame.DeviceLevelMaster, "master", nil
	case "slave":
		return frame.DeviceLevelSlave, "slave", nil
	default:
		return 0, "", metadb.ErrInvalidArgument
	}
}

func managerDeviceFlag(flag frame.DeviceFlag) string {
	switch flag {
	case frame.APP:
		return "app"
	case frame.WEB:
		return "web"
	case frame.PC:
		return "pc"
	case frame.SYSTEM:
		return "system"
	default:
		return "unknown"
	}
}

func managerConnectionDeviceLevel(level frame.DeviceLevel) string {
	switch level {
	case frame.DeviceLevelMaster:
		return "master"
	case frame.DeviceLevelSlave:
		return "slave"
	default:
		return "unknown"
	}
}

func validateUserListCursor(cursor UserListCursor, keywordHash uint32) error {
	if cursor == (UserListCursor{}) {
		return nil
	}
	if cursor.SlotID == 0 || cursor.UID == "" {
		return metadb.ErrInvalidArgument
	}
	if cursor.KeywordHash != keywordHash {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func newUserListCursor(slotID uint32, cursor metadb.UserCursor, keyword string) UserListCursor {
	return UserListCursor{SlotID: slotID, UID: cursor.UID, KeywordHash: userKeywordHash(keyword)}
}

func userListCursorForItem(item UserListItem, keyword string) UserListCursor {
	return UserListCursor{SlotID: item.SlotID, UID: item.UID, KeywordHash: userKeywordHash(keyword)}
}

func (c UserListCursor) shardCursor() metadb.UserCursor {
	return metadb.UserCursor{UID: c.UID}
}

func userHashSlot(table control.HashSlotTable, uid string) uint16 {
	return routing.HashSlotForKey(uid, table.Count)
}

func userKeywordHash(keyword string) uint32 {
	return crc32.ChecksumIEEE([]byte(keyword))
}

func userFilteredScanLimit(limit int) int {
	if limit > defaultUserListInternalScanLimit {
		return limit
	}
	return defaultUserListInternalScanLimit
}

func generateManagerUserToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
