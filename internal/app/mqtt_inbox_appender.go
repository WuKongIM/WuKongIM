package app

import (
	"context"
	"time"

	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// newMQTTInboxAppender wires the shared durable seam to the existing native
// directory worker. It owns no extra queue and never opens an offline owner scope.
func newMQTTInboxAppender(node *cluster.Node, ids interface{ Next() uint64 }, next sessioncase.InboxAppendNext, wake func()) (*sessioncase.InboxAppender, error) {
	if node == nil || wake == nil {
		return nil, sessioncase.ErrInvalid
	}
	admission, err := newMQTTInboxAdmission(node, ids)
	if err != nil {
		return nil, err
	}
	return sessioncase.NewInboxAppender(sessioncase.InboxAppenderOptions{
		Admission: admission,
		Directory: mqttInboxAppendDirectory{node: node, wake: wake},
		Next:      next,
	})
}

// mqttInboxAppendDirectory only adapts the entry-independent source identity to
// native directory admission. Completion is checked by the usecase's fresh read.
type mqttInboxAppendDirectory struct {
	node *cluster.Node
	wake func()
}

func (d mqttInboxAppendDirectory) Admit(ctx context.Context, ch sessioncase.SourceChannel) error {
	results := d.node.AdmitPersonDirectoryTasks(ctx, []meta.PersonDirectoryTask{{ChannelID: ch.ID, ChannelType: int64(ch.Type), CreatedAt: time.Now().UnixMilli()}})
	if len(results) != 1 {
		return sessioncase.ErrEvidence
	}
	if results[0] != nil {
		return results[0]
	}
	d.wake()
	return nil
}
