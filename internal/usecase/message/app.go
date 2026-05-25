package message

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/runtime/userlimit"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var (
	ErrUnauthenticatedSender             = errors.New("usecase/message: unauthenticated sender")
	ErrChannelAppenderRequired           = errors.New("usecase/message: channel appender required")
	ErrSyncLoginUIDRequired              = errors.New("login_uid不能为空！")
	ErrSyncChannelIDRequired             = errors.New("channel_id不能为空！")
	ErrSyncChannelTypeRequired           = errors.New("channel_type不能为空！")
	ErrMessageReaderRequired             = errors.New("usecase/message: message reader required")
	ErrRequestSubscribersRequireSyncOnce = errors.New("usecase/message: request subscribers require sync_once")
	ErrRequestSubscribersConflictChannel = errors.New("usecase/message: request subscribers cannot include channel_id")
	ErrRequestSubscribersRequired        = errors.New("usecase/message: request subscribers required")
	ErrMessageIDGeneratorRequired        = errors.New("usecase/message: message id generator required")
	ErrRealtimeDispatcherRequired        = errors.New("usecase/message: realtime dispatcher required")
	ErrCommittedDispatcherRequired       = errors.New("usecase/message: committed dispatcher required")
)

type Options struct {
	IdentityStore IdentityStore
	ChannelStore  ChannelStore
	// ChannelAppender owns durable channel append routing.
	ChannelAppender     ChannelAppender
	MessageReader       ChannelMessageReader
	Online              online.Registry
	Delivery            online.Delivery
	Recipients          RecipientDirectory
	RemoteDelivery      RemoteDelivery
	CommittedDispatcher CommittedMessageDispatcher
	// CMDConversationIntents accepts durable request-scoped CMD sync intents.
	CMDConversationIntents CMDConversationIntentSink
	RealtimeDispatcher     RealtimeDispatcher
	SendHook               SendHook
	MessageIDs             MessageIDGenerator
	DeliveryAck            DeliveryAck
	DeliveryOffline        DeliveryOffline
	PermissionStore        PermissionStore
	SystemUIDs             SystemUIDChecker
	// UserSendLimiter rejects over-limit sends before expensive permission, hook, and append work.
	UserSendLimiter UserSendLimiter
	// PersonWhitelistEnabled enables receiver-side personal allowlist checks.
	PersonWhitelistEnabled bool
	// SystemDeviceID identifies trusted system-device sessions after SendBan passes.
	SystemDeviceID string
	// PermissionCacheTTL enables a bounded read-through permission cache for channel,
	// membership, and missing-channel reads. Zero keeps permission reads uncached.
	PermissionCacheTTL time.Duration
	// AppendMetrics records durable append attempts without coupling usecases to a metrics backend.
	AppendMetrics messageAppendMetrics
	LocalNodeID   uint64
	LocalBootID   uint64
	Now           func() time.Time
	Logger        wklog.Logger
}

type App struct {
	identities             IdentityStore
	channels               ChannelStore
	appender               ChannelAppender
	messageReader          ChannelMessageReader
	online                 online.Registry
	delivery               online.Delivery
	recipients             RecipientDirectory
	remote                 RemoteDelivery
	dispatcher             CommittedMessageDispatcher
	cmdConversationIntents CMDConversationIntentSink
	realtime               RealtimeDispatcher
	sendHook               SendHook
	messageIDs             MessageIDGenerator
	deliveryAck            DeliveryAck
	deliveryOffline        DeliveryOffline
	permissions            PermissionStore
	systemUIDs             SystemUIDChecker
	userSendLimiter        UserSendLimiter
	personWhitelistEnabled bool
	systemDeviceID         string
	appendMetrics          messageAppendMetrics
	localNodeID            uint64
	localBootID            uint64
	now                    func() time.Time
	logger                 wklog.Logger
}

func New(opts Options) *App {
	if opts.Online == nil {
		opts.Online = online.NewRegistry()
	}
	if opts.Delivery == nil {
		opts.Delivery = online.LocalDelivery{}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	permissions := newPermissionCache(opts.PermissionStore, opts.PermissionCacheTTL, opts.Now)

	return &App{
		identities:             opts.IdentityStore,
		channels:               opts.ChannelStore,
		appender:               opts.ChannelAppender,
		messageReader:          opts.MessageReader,
		online:                 opts.Online,
		delivery:               opts.Delivery,
		recipients:             opts.Recipients,
		remote:                 opts.RemoteDelivery,
		dispatcher:             opts.CommittedDispatcher,
		cmdConversationIntents: opts.CMDConversationIntents,
		realtime:               opts.RealtimeDispatcher,
		sendHook:               opts.SendHook,
		messageIDs:             opts.MessageIDs,
		deliveryAck:            opts.DeliveryAck,
		deliveryOffline:        opts.DeliveryOffline,
		permissions:            permissions,
		systemUIDs:             opts.SystemUIDs,
		userSendLimiter:        opts.UserSendLimiter,
		personWhitelistEnabled: opts.PersonWhitelistEnabled,
		systemDeviceID:         opts.SystemDeviceID,
		appendMetrics:          opts.AppendMetrics,
		localNodeID:            opts.LocalNodeID,
		localBootID:            opts.LocalBootID,
		now:                    opts.Now,
		logger:                 opts.Logger,
	}
}

func (a *App) OnlineRegistry() online.Registry {
	if a == nil {
		return nil
	}
	return a.online
}

func (a *App) sendLogger() wklog.Logger {
	if a == nil || a.logger == nil {
		return wklog.NewNop()
	}
	return a.logger.Named("send")
}

type IdentityStore interface {
	GetUser(ctx context.Context, uid string) (metadb.User, error)
}

type ChannelStore interface {
	GetChannel(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error)
}

// PermissionStore provides authoritative membership and channel reads for send authorization.
type PermissionStore interface {
	GetChannelForPermission(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error)
	ContainsChannelSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (bool, error)
	HasChannelSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error)
}

// SystemUIDChecker identifies internal system senders that bypass business permissions.
type SystemUIDChecker interface {
	IsSystemUID(uid string) bool
}

// UserSendLimiter decides whether a user-origin send can enter the expensive send path.
type UserSendLimiter interface {
	AllowSend(now time.Time, req userlimit.Request) userlimit.Decision
}
