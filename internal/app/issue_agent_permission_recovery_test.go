package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	issueagentgithub "github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestCurrentAuthorizationRetriesTrustedIssueAuthorPermission(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name            string
		firstPermission string
	}{
		{name: "stale read", firstPermission: "read"},
		{name: "transient API error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				require.Equal(t,
					"/repos/WuKongIM/WuKongIM/collaborators/reporter/permission",
					request.URL.Path,
				)
				if attempts.Add(1) == 1 && test.firstPermission == "" {
					http.Error(writer, "transient failure", http.StatusInternalServerError)
					return
				}
				permission := "admin"
				if attempts.Load() == 1 {
					permission = test.firstPermission
				}
				writer.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
					"permission": permission,
					"user":       map[string]string{"login": "reporter"},
				}))
			}))
			t.Cleanup(server.Close)
			client, err := issueagentgithub.NewClient(
				issueagentgithub.ClientConfig{
					BaseURL: server.URL, Repository: "WuKongIM/WuKongIM",
					Token: "token", MaxPages: 2, MaxBodyBytes: 1 << 20,
				},
				server.Client(),
			)
			require.NoError(t, err)

			authorization, permission, err := currentAuthorization(
				context.Background(),
				client,
				issueagentgithub.IssueFacts{
					Number: 42, Author: "reporter", AuthorAssociation: "MEMBER",
				},
				nil,
				nil,
			)
			require.NoError(t, err)
			require.NotNil(t, authorization)
			require.Equal(t, "reporter", authorization.Actor)
			require.Equal(t, "admin", authorization.Permission)
			require.Equal(t, "issue:42", authorization.EventID)
			require.Empty(t, authorization.Command)
			require.Equal(t, "admin", permission)
			require.Equal(t, int32(2), attempts.Load())
		})
	}
}

func TestCurrentAuthorizationStopsTrustedPermissionRecoveryOnCancellation(
	t *testing.T,
) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		attempts.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"permission": "read",
			"user":       map[string]string{"login": "reporter"},
		}))
	}))
	t.Cleanup(server.Close)
	client, err := issueagentgithub.NewClient(
		issueagentgithub.ClientConfig{
			BaseURL: server.URL, Repository: "WuKongIM/WuKongIM",
			Token: "token", MaxPages: 2, MaxBodyBytes: 1 << 20,
		},
		server.Client(),
	)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	authorization, _, err := currentAuthorization(
		ctx,
		client,
		issueagentgithub.IssueFacts{
			Number: 42, Author: "reporter", AuthorAssociation: "MEMBER",
		},
		nil,
		nil,
	)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, authorization)
	require.GreaterOrEqual(t, attempts.Load(), int32(2))
}
