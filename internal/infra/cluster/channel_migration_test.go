package cluster

import (
	"context"
	"errors"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestNewChannelMigrationStoreReadsNodeStoreLazily(t *testing.T) {
	id := channelruntime.ChannelID{ID: "lazy-migration", Type: 2}
	node := &fakeChannelMigrationStoreNode{}

	got := NewChannelMigrationStore(node)
	if got == nil {
		t.Fatal("NewChannelMigrationStore() = nil, want lazy adapter")
	}
	_, _, err := got.GetActive(context.Background(), id)
	if !errors.Is(err, managementusecase.ErrChannelMigrationUnavailable) {
		t.Fatalf("GetActive(before store) err = %v, want ErrChannelMigrationUnavailable", err)
	}

	task := metadb.ChannelMigrationTask{TaskID: "task-lazy", ChannelID: id.ID, ChannelType: int64(id.Type)}
	node.store = channels.NewMigrationStore(channels.MigrationStoreConfig{
		Router: fakeChannelMigrationRouter{route: routing.Route{HashSlot: 3, SlotID: 1, Leader: 1}},
		Reader: fakeChannelMigrationReader{
			task: task,
			ok:   true,
		},
	})

	active, ok, err := got.GetActive(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("GetActive(after store) ok=%v err=%v", ok, err)
	}
	if active.TaskID != task.TaskID {
		t.Fatalf("active task id = %q, want %q", active.TaskID, task.TaskID)
	}
}

type fakeChannelMigrationStoreNode struct {
	store *channels.MigrationStore
}

func (f *fakeChannelMigrationStoreNode) ChannelMigrationStore() *channels.MigrationStore {
	return f.store
}

type fakeChannelMigrationRouter struct {
	route routing.Route
}

func (f fakeChannelMigrationRouter) RouteKey(string) (routing.Route, error) {
	return f.route, nil
}

type fakeChannelMigrationReader struct {
	task metadb.ChannelMigrationTask
	ok   bool
}

func (f fakeChannelMigrationReader) GetChannelRuntimeMeta(context.Context, uint16, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
}

func (f fakeChannelMigrationReader) GetActiveChannelMigrationTask(context.Context, uint16, string, int64) (metadb.ChannelMigrationTask, bool, error) {
	return f.task, f.ok, nil
}

func (f fakeChannelMigrationReader) GetChannelMigrationTask(context.Context, uint16, string, int64, string) (metadb.ChannelMigrationTask, bool, error) {
	return f.task, f.ok, nil
}

func (f fakeChannelMigrationReader) ListActiveChannelMigrationTasks(context.Context, uint16, int) ([]metadb.ChannelMigrationTask, error) {
	if !f.ok {
		return nil, nil
	}
	return []metadb.ChannelMigrationTask{f.task}, nil
}
