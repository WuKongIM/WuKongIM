package reviewagent_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

func TestGenerationIdentityRequiresExactImmutableCoordinates(t *testing.T) {
	t.Parallel()

	valid := validGeneration()
	require.NoError(t, reviewagent.ValidateGenerationIdentity(valid))

	tests := map[string]func(*reviewagent.GenerationIdentity){
		"repository": func(identity *reviewagent.GenerationIdentity) {
			identity.Repository = "WuKongIM"
		},
		"pull request": func(identity *reviewagent.GenerationIdentity) {
			identity.PullRequest = 0
		},
		"head": func(identity *reviewagent.GenerationIdentity) {
			identity.HeadSHA = strings.Repeat("a", 39)
		},
		"base": func(identity *reviewagent.GenerationIdentity) {
			identity.BaseSHA = strings.Repeat("b", 41)
		},
		"test merge": func(identity *reviewagent.GenerationIdentity) {
			identity.TestMergeSHA = strings.Repeat("G", 40)
		},
		"intent": func(identity *reviewagent.GenerationIdentity) {
			identity.IntentDigest = "sha256:short"
		},
		"generation": func(identity *reviewagent.GenerationIdentity) {
			identity.Generation = 0
		},
		"state parent": func(identity *reviewagent.GenerationIdentity) {
			identity.StateParentSHA = ""
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			identity := valid
			mutate(&identity)
			require.Error(t, reviewagent.ValidateGenerationIdentity(identity))
		})
	}
}

func TestIntentDigestIsCanonicalAndBindsSemanticInput(t *testing.T) {
	t.Parallel()

	first, err := reviewagent.IntentDigest(
		"Fix delivery race\r\n",
		"Preserve ordering.  \r\n",
		[]string{"docs/spec.md", "#42"},
	)
	require.NoError(t, err)

	reordered, err := reviewagent.IntentDigest(
		"Fix delivery race\n",
		"Preserve ordering.\n",
		[]string{"#42", "docs/spec.md"},
	)
	require.NoError(t, err)
	require.Equal(t, first, reordered)

	changed, err := reviewagent.IntentDigest(
		"Fix delivery race\n",
		"Allow reordering.\n",
		[]string{"#42", "docs/spec.md"},
	)
	require.NoError(t, err)
	require.NotEqual(t, first, changed)
}

func validGeneration() reviewagent.GenerationIdentity {
	return reviewagent.GenerationIdentity{
		Repository:     "WuKongIM/WuKongIM",
		PullRequest:    42,
		HeadSHA:        strings.Repeat("a", 40),
		BaseSHA:        strings.Repeat("b", 40),
		TestMergeSHA:   strings.Repeat("c", 40),
		IntentDigest:   "sha256:" + strings.Repeat("d", 64),
		Generation:     3,
		StateParentSHA: strings.Repeat("e", 40),
	}
}
