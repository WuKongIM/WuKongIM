package proxy

import (
	"strings"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTAccountingReplyRequiresExactHeadAndExclusiveShape(t *testing.T) {
	key := metadb.MQTTDeliveryCursorKey{Namespace: "main", ClientID: "client", SessionGeneration: 1, SubscriptionGeneration: 2, SourceKind: 1, SourceID: "2:g", SourceGeneration: "source"}
	q := metadb.MQTTRead{Kind: metadb.MQTTReadAccounting, CursorKey: key}
	require.Equal(t, metadb.MQTTReadKind(18), q.Kind)
	for _, mode := range []string{"valid", "absent", "missing", "foreign", "extra", "partial", "head", "version", "empty", "tail", "counter"} {
		t.Run(mode, func(t *testing.T) {
			c := metadb.MQTTDeliveryCursor{Key: key, Topic: "topic", StartAfter: 100, AccountedThrough: 104, WindowThrough: 100, CompletedThrough: 100, PendingMessages: 1, PendingBytes: 5, Revision: 4, LastMutationDigest: strings.Repeat("a", 64), UpdatedAtMS: 1000, AccountingVersion: 1, AccountingHead: 101, AccountingTail: 101}
			r := metadb.MQTTReadResult{Done: true, DeliveryCursors: []metadb.MQTTDeliveryCursor{c}, Accounting: &metadb.MQTTAccountingRange{Key: key, From: 101, Through: 104, SubscriptionRevision: 2, EvaluatedAtMS: 1000, Items: []metadb.MQTTAccountingItem{{Position: 103, Bytes: 5}}}}
			switch mode {
			case "absent":
				r.DeliveryCursors = nil
				r.Accounting = nil
			case "missing":
				r.Accounting = nil
			case "foreign":
				r.Accounting.Key.ClientID = "foreign"
			case "extra":
				r.Inflight = []metadb.MQTTInflight{{}}
			case "partial":
				r.Done = false
			case "head":
				r.Accounting.From = 102
			case "version":
				r.DeliveryCursors[0].AccountingVersion = 0
			case "empty":
				r.Accounting.Items = nil
			case "tail":
				r.Accounting.NextFrom = 105
			case "counter":
				r.Accounting.Items[0].Bytes = 6
			}
			err := validateMQTTReadShape(q, r)
			if mode == "valid" || mode == "absent" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if mode == "valid" {
				q.Kind = metadb.MQTTReadDeliveryCursor
				require.Error(t, validateMQTTReadShape(q, r))
				q.Kind = metadb.MQTTReadAccounting
			}
		})
	}
}
