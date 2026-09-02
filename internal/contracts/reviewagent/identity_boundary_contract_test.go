package reviewagent_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

func TestGenerationDigestIsStableAndRejectsPartialIdentity(t *testing.T) {
	t.Parallel()

	identity := validGeneration()
	first, err := reviewagent.GenerationIdentityDigest(identity)
	require.NoError(t, err)
	second, err := reviewagent.GenerationIdentityDigest(identity)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, first, reviewagent.MustGenerationDigest(identity))

	identity.PullRequest = 0
	_, err = reviewagent.GenerationIdentityDigest(identity)
	require.Error(t, err)
	require.Panics(t, func() {
		reviewagent.MustGenerationDigest(identity)
	})
}

func TestIntentDigestRejectsAmbiguousOrUnboundedInput(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		title string
		body  string
		links []string
	}{
		"missing title": {
			body: "body",
		},
		"title exceeds bound": {
			title: strings.Repeat("t", 1025),
		},
		"body exceeds bound": {
			title: "title",
			body:  strings.Repeat("b", (64<<10)+1),
		},
		"duplicate locator": {
			title: "title",
			links: []string{"#7", "#7"},
		},
		"empty locator": {
			title: "title",
			links: []string{""},
		},
		"too many locators": {
			title: "title",
			links: make([]string, 65),
		},
		"invalid UTF-8 title": {
			title: string([]byte{0xff}),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := reviewagent.IntentDigest(
				test.title,
				test.body,
				test.links,
			)
			require.Error(t, err)
		})
	}
}

func TestLinkedIssueIntentLocatorBindsAllFetchedIntentFacts(t *testing.T) {
	t.Parallel()

	issue := reviewagent.LinkedIssue{
		Number: 42,
		State:  "open",
		Title:  "Prevent duplicate delivery",
		Body:   "The retry path can publish twice.",
	}
	first, err := reviewagent.LinkedIssueIntentLocator(issue)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(first, "#42:sha256:"))
	second, err := reviewagent.LinkedIssueIntentLocator(issue)
	require.NoError(t, err)
	require.Equal(t, first, second)

	issue.State = "closed"
	closed, err := reviewagent.LinkedIssueIntentLocator(issue)
	require.NoError(t, err)
	require.NotEqual(t, first, closed)

	for name, invalid := range map[string]reviewagent.LinkedIssue{
		"number": {Number: 0, State: "open", Title: "title"},
		"state":  {Number: 1, State: "merged", Title: "title"},
		"title":  {Number: 1, State: "open"},
		"body": {
			Number: 1,
			State:  "open",
			Title:  "title",
			Body:   "bad\x00body",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, locatorErr := reviewagent.LinkedIssueIntentLocator(invalid)
			require.Error(t, locatorErr)
		})
	}
}
