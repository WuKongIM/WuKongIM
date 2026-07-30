package issueagentgithub_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	issueagentgithub "github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestVersionSourceResolverReadsCommitLightweightAndAnnotatedTags(t *testing.T) {
	t.Parallel()

	commit := fortyHex("a")
	tagObject := fortyHex("b")
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/WuKongIM/WuKongIM/git/commits/" + commit:
			writeJSON(t, writer, map[string]any{"sha": commit})
		case "/repos/WuKongIM/WuKongIM/git/ref/tags/v2.1.0":
			writeJSON(t, writer, map[string]any{
				"ref":    "refs/tags/v2.1.0",
				"object": map[string]any{"type": "commit", "sha": commit},
			})
		case "/repos/WuKongIM/WuKongIM/git/ref/tags/v2.2.0":
			writeJSON(t, writer, map[string]any{
				"ref":    "refs/tags/v2.2.0",
				"object": map[string]any{"type": "tag", "sha": tagObject},
			})
		case "/repos/WuKongIM/WuKongIM/git/tags/" + tagObject:
			writeJSON(t, writer, map[string]any{
				"sha":    tagObject,
				"object": map[string]any{"type": "commit", "sha": commit},
			})
		default:
			writer.WriteHeader(http.StatusNotFound)
			writeJSON(t, writer, map[string]any{"message": "Not Found"})
		}
	}))
	t.Cleanup(server.Close)
	resolver, err := issueagentgithub.NewVersionSourceResolver(
		newTestClient(t, server),
	)
	require.NoError(t, err)

	exists, err := resolver.CommitExists(context.Background(), commit)
	require.NoError(t, err)
	require.True(t, exists)
	for _, tag := range []string{"v2.1.0", "v2.2.0"} {
		candidates, err := resolver.ResolveTag(context.Background(), tag)
		require.NoError(t, err)
		require.Equal(t, []string{commit}, candidates)
	}
}

func TestVersionSourceResolverRejectsMissingOrNonCommitTag(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/WuKongIM/WuKongIM/git/ref/tags/v2.1.0":
			writeJSON(t, writer, map[string]any{
				"ref":    "refs/tags/v2.1.0",
				"object": map[string]any{"type": "tree", "sha": fortyHex("d")},
			})
		default:
			writer.WriteHeader(http.StatusNotFound)
			writeJSON(t, writer, map[string]any{"message": "Not Found"})
		}
	}))
	t.Cleanup(server.Close)
	resolver, err := issueagentgithub.NewVersionSourceResolver(
		newTestClient(t, server),
	)
	require.NoError(t, err)

	exists, err := resolver.CommitExists(context.Background(), fortyHex("a"))
	require.NoError(t, err)
	require.False(t, exists)
	_, err = resolver.ResolveTag(context.Background(), "v2.1.0")
	require.Error(t, err)
	missing, err := resolver.ResolveTag(context.Background(), "v9.9.9")
	require.NoError(t, err)
	require.Empty(t, missing)
}
