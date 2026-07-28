package issueagentgithub_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestCheckpointPublicKeyFileIsStrictAndSafeWhileDisabled(t *testing.T) {
	t.Parallel()

	file, err := os.Open("../../../.github/issue-agent/checkpoint-public-keys.json")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })

	keySet, err := issueagentgithub.DecodeKeySet(file, 64<<10)
	require.NoError(t, err)
	require.Equal(t, 1, keySet.SchemaVersion)
	require.Empty(t, keySet.Keys)
}

func TestCheckpointStoreVerifiesAppendOnlySignedChain(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	store, err := issueagentgithub.NewCheckpointStore(
		"WuKongIM/WuKongIM",
		"wukongim-issue-agent[bot]",
		issueagentgithub.KeySet{SchemaVersion: 1, Keys: []issueagentgithub.PublicKey{{
			ID:        "checkpoint-2026-07",
			PublicKey: publicKey,
			NotBefore: now.Add(-time.Hour),
			NotAfter:  now.Add(24 * time.Hour),
		}}},
		issueagentgithub.Signer{
			KeyID:      "checkpoint-2026-07",
			PrivateKey: privateKey,
		},
	)
	require.NoError(t, err)

	first := checkpointStoreTestCheckpoint()
	firstBody, firstDigest, err := store.SignComment(first, "Authorized for reproduction.")
	require.NoError(t, err)

	second := first
	second.Sequence = 2
	second.State = issueagent.StateVersionPinned
	second.NextAction = issueagent.ActionReproduce
	second.ExpectedPreviousCheckpointID = pointer(int64(501))
	second.PreviousCheckpointSHA256 = pointer(firstDigest)
	second.Versions.AffectedSHA = "89abcdef0123456789abcdef0123456789abcdef"
	secondBody, _, err := store.SignComment(second, "Pinned both source revisions.")
	require.NoError(t, err)

	verified, err := store.VerifyChain([]issueagentgithub.IssueComment{
		{
			ID: 501, Author: "wukongim-issue-agent[bot]", AuthorType: "Bot",
			Body: firstBody, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: 502, Author: "wukongim-issue-agent[bot]", AuthorType: "Bot",
			Body: secondBody, CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute),
		},
	}, 42, now.Add(2*time.Minute))
	require.NoError(t, err)
	require.Equal(t, int64(502), verified.CommentID)
	require.Equal(t, uint64(2), verified.Checkpoint.Sequence)
	require.Equal(t, issueagent.StateVersionPinned, verified.Checkpoint.State)
}

func TestCheckpointStoreFailsClosedOnMutationEditAndWrongAuthor(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	store, err := issueagentgithub.NewCheckpointStore(
		"WuKongIM/WuKongIM",
		"wukongim-issue-agent[bot]",
		issueagentgithub.KeySet{SchemaVersion: 1, Keys: []issueagentgithub.PublicKey{{
			ID: "key", PublicKey: publicKey,
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		}}},
		issueagentgithub.Signer{KeyID: "key", PrivateKey: privateKey},
	)
	require.NoError(t, err)
	body, _, err := store.SignComment(checkpointStoreTestCheckpoint(), "Authorized.")
	require.NoError(t, err)

	tests := []struct {
		name    string
		comment issueagentgithub.IssueComment
	}{
		{
			name: "mutation",
			comment: issueagentgithub.IssueComment{
				ID: 501, Author: "wukongim-issue-agent[bot]", AuthorType: "Bot",
				Body: strings.Replace(
					body, "WuKongIM/WuKongIM", "WuKongIM/Other", 1,
				),
				CreatedAt: now, UpdatedAt: now,
			},
		},
		{
			name: "edited",
			comment: issueagentgithub.IssueComment{
				ID: 501, Author: "wukongim-issue-agent[bot]", AuthorType: "Bot",
				Body: body, CreatedAt: now, UpdatedAt: now.Add(time.Second),
			},
		},
		{
			name: "wrong author",
			comment: issueagentgithub.IssueComment{
				ID: 501, Author: "attacker", AuthorType: "User",
				Body: body, CreatedAt: now, UpdatedAt: now,
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := store.VerifyChain([]issueagentgithub.IssueComment{
				test.comment,
			}, 42, now)
			require.Error(t, err)
		})
	}
}

func checkpointStoreTestCheckpoint() issueagent.Checkpoint {
	return issueagent.Checkpoint{
		SchemaVersion: 1,
		Repository:    "WuKongIM/WuKongIM",
		IssueNumber:   42,
		Generation:    1,
		Sequence:      1,
		State:         issueagent.StateAuthorized,
		FrozenInput: issueagent.FrozenInput{
			IssueBodySHA256:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			AffectedVersion:    "v2.0.0",
			AcceptedCommentIDs: []int64{},
			AuthorizationEvent: "evt-42",
			AuthorizedBy:       "maintainer",
		},
		Versions: issueagent.Versions{
			ReportedRef:      "v2.0.0",
			DiagnosisBaseSHA: "0123456789abcdef0123456789abcdef01234567",
		},
		Budget:     issueagent.Budget{},
		NextAction: issueagent.ActionPinVersions,
	}
}

func pointer[T any](value T) *T {
	return &value
}
