package user

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var (
	ErrUserStoreRequired   = errors.New("usecase/user: user store required")
	ErrDeviceStoreRequired = errors.New("usecase/user: device store required")
)

type UserStore interface {
	GetUser(ctx context.Context, uid string) (metadb.User, error)
	CreateUser(ctx context.Context, u metadb.User) error
}

type DeviceStore interface {
	UpsertDevice(ctx context.Context, d metadb.Device) error
}

type Options struct {
	Users     UserStore
	Devices   DeviceStore
	Online    online.Registry
	AfterFunc func(time.Duration, func())
	Logger    wklog.Logger
}
