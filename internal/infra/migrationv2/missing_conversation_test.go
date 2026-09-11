package migrationv2_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

// Only private fixture copies lose their conversations; source members/history stay intact.
func missingConversationFixture(t *testing.T) string {
	source := compatibleMessageFixture(t)
	require.Positive(t, rewriteOriginalIndexFixture(t, source, func(k, v []byte, b *pebble.Batch) bool {
		if len(k) >= 4 && binary.BigEndian.Uint16(k) == 0x0901 {
			require.NoError(t, b.Delete(k, nil))
			return true
		}
		return false
	}))
	return source
}
func missingPin(capture, uid string) migration.MissingConversationRecovery {
	hash := func(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
	return migration.MissingConversationRecovery{CaptureDigest: capture, UIDSHA256: hash(uid), ChannelSHA256: hash(migration.IdentityKey("migrationgroup", uint8(2))), RetainedTail: 3}
}

func TestMissingConversationRecoveryNativeArchiveAndIndependentVerification(t *testing.T) {
	for _, nodes := range []int{1, 3} {
		for _, visibility := range []string{"", "hidden_until_new_message"} {
			t.Run(fmt.Sprintf("single_node_source_to_%d_node_cluster_%s", nodes, visibility), func(t *testing.T) {
				ctx := context.Background()
				r := migrationv2.Reader{}
				source := missingConversationFixture(t)
				before := fileDigests(t, source)
				plan := diagnosticPlan(t, source)
				plan.Metadata = conversationPolicy()
				plan.Messages = &migration.MessagePolicy{KeepLatestDuplicates: true, ExcludeCMD: true, CompactSequences: true}
				plan.Target.Replicas, plan.Target.ChannelReplicas = uint16(nodes), uint16(nodes)
				for n := 2; n <= nodes; n++ {
					plan.Target.Nodes = append(plan.Target.Nodes, migration.TargetNode{NodeID: uint64(100 + n), Addr: fmt.Sprintf("127.0.0.1:%d", 58000+n), DataDir: filepath.Join(t.TempDir(), "target")})
				}
				open := func(p migration.Plan) *transfer.Spool {
					w, e := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), p.Digest(), 128<<20)
					require.NoError(t, e)
					t.Cleanup(func() { require.NoError(t, w.Close()) })
					return w
				}
				strict, e := migration.Prepare(ctx, plan, open(plan), r, r, nil)
				require.ErrorContains(t, e, "visibility compatibility")
				plan.Metadata.MissingConversations = []migration.MissingConversationRecovery{missingPin(strict.Capture.Digest, "migrationalice"), missingPin(strict.Capture.Digest, "migrationbob")}
				for i := range plan.Metadata.MissingConversations {
					plan.Metadata.MissingConversations[i].Visibility = visibility
				}
				w := open(plan)
				prepared, e := migration.Prepare(ctx, plan, w, r, r, nil)
				require.NoError(t, e)
				again, e := migration.Prepare(ctx, plan, w, r, r, nil)
				require.NoError(t, e)
				require.Equal(t, prepared, again)
				wantRead, wantHidden := uint64(3), uint64(0)
				if visibility != "" {
					wantRead, wantHidden = 0, 3
				}
				count := 0
				require.NoError(t, migration.WalkTargetMetadata(ctx, w, func(row migration.TargetRecord) error {
					if row.Table == "membership" {
						var m meta.UserChannelMembership
						require.NoError(t, migration.UnmarshalState(row.Value, &m))
						require.Equal(t, meta.UserChannelMembership{UID: m.UID, ChannelID: "migrationgroup", ChannelType: 2, JoinSeq: 1, ReadSeq: wantRead, ConversationHiddenThroughSeq: wantHidden, SourceVersion: 1}, m)
						count++
					}
					return nil
				}))
				require.Equal(t, 2, count)
				require.EqualValues(t, 3, prepared.Conversion.Messages)
				archive, e := archivefs.NewFileArchiveStore(filepath.Join(t.TempDir(), "archive"))
				require.NoError(t, e)
				_, e = migration.ExportSourceArchive(ctx, migration.SourceArchiveOptions{PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit}, prepared.Capture, prepared.Catalog, prepared.Selection, w, archive)
				require.NoError(t, e)
				require.Equal(t, before, fileDigests(t, source))
				require.NoError(t, os.Rename(source, source+"-unmounted"))
				fresh := open(plan)
				rebuilt, e := migration.PrepareArchive(ctx, plan, fresh, r, archive)
				require.NoError(t, e)
				require.Equal(t, prepared.Conversion, rebuilt.Conversion)
				require.NoError(t, migrationv3.Install(ctx, plan.Target, rebuilt.Conversion, fresh))
				require.NoError(t, migrationv3.Install(ctx, plan.Target, rebuilt.Conversion, fresh))
				verified, e := migration.VerifyTargets(ctx, plan.Target, rebuilt.Selection, fresh, r, migrationv3.Inspector{})
				require.NoError(t, e)
				require.EqualValues(t, 3*nodes, verified.Messages)
				for _, field := range []string{"read_seq", "deleted_to_seq", "join_seq", "conversation_hidden_through_seq"} {
					_, e = migration.VerifyTargets(ctx, plan.Target, rebuilt.Selection, fresh, r, missingConversationFault{field})
					require.ErrorContains(t, e, "user_channel_membership")
				}
			})
		}
	}
}

func TestMissingConversationRecoveryRejectsDriftAndUnapprovedMembers(t *testing.T) {
	for _, mode := range []string{"capture", "tail", "uid", "existing", "pending"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			r := migrationv2.Reader{}
			source := missingConversationFixture(t)
			if mode == "existing" {
				source = compatibleMessageFixture(t)
			}
			if mode == "pending" {
				require.NoError(t, os.WriteFile(filepath.Join(source, "conversationv2", "conversations.json"), []byte(`[{"channel_id":"migrationgroup","channel_type":2,"user_read_seqs":{"migrationbob":3},"tag_key":"synthetic-cache","last_msg_seq":3}]`), 0600))
			}
			p := diagnosticPlan(t, source)
			p.Metadata = conversationPolicy()
			open := func() *transfer.Spool {
				w, e := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), p.Digest(), 128<<20)
				require.NoError(t, e)
				t.Cleanup(func() { require.NoError(t, w.Close()) })
				return w
			}
			captured, e := migration.CaptureSources(ctx, p.Sources, r, open(), nil)
			require.NoError(t, e)
			pins := []migration.MissingConversationRecovery{missingPin(captured.Digest, "migrationalice"), missingPin(captured.Digest, "migrationbob")}
			want := ""
			switch mode {
			case "capture":
				pins[0].CaptureDigest = fmt.Sprintf("%064x", 1)
				want = "capture differs"
			case "tail":
				pins[0].RetainedTail = 4
				want = "retained tail differs"
			case "uid":
				pins = pins[:1]
				want = "visibility compatibility"
			case "existing", "pending":
				want = "exists in original rows"
			}
			p.Metadata.MissingConversations = pins
			_, e = migration.Prepare(ctx, p, open(), r, r, nil)
			require.ErrorContains(t, e, want)
			require.NoDirExists(t, p.Target.Nodes[0].DataDir)
		})
	}
}

type missingConversationFault struct{ field string }

func (f missingConversationFault) Open(ctx context.Context, p migration.TargetPlan, n migration.TargetNode) (migration.TargetView, error) {
	v, e := (migrationv3.Inspector{}).Open(ctx, p, n)
	if e != nil {
		return nil, e
	}
	return missingConversationFaultView{v, f.field}, nil
}

type missingConversationFaultView struct {
	migration.TargetView
	field string
}

func (v missingConversationFaultView) Metadata(ctx context.Context, table, owner string, key map[string]any) (map[string]any, bool, error) {
	got, found, e := v.TargetView.Metadata(ctx, table, owner, key)
	if e == nil && found && table == "user_channel_membership" {
		if fmt.Sprint(got[v.field]) == "0" {
			got[v.field] = uint64(3)
		} else {
			got[v.field] = uint64(0)
		}
	}
	return got, found, e
}
