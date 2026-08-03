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

func TestCurrentAuthorizationUsesPermissionWhenAuthorAssociationLags(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		attempts.Add(1)
		require.Equal(t,
			"/repos/WuKongIM/WuKongIM/collaborators/reporter/permission",
			request.URL.Path,
		)
		writer.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"permission": "admin",
			"user":       map[string]string{"login": "reporter"},
		}))
	}))
	client := issueAuthorPermissionTestClient(t, server)

	authorization, permission, err := currentAuthorization(
		context.Background(),
		client,
		issueagentgithub.IssueFacts{
			Number: 42, Author: "reporter", AuthorAssociation: "CONTRIBUTOR",
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
	require.Equal(t, int32(1), attempts.Load())
}

func TestCurrentAuthorizationRetriesTransientIssueAuthorPermission(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if attempts.Add(1) == 1 {
			http.Error(writer, "transient failure", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"permission": "admin",
			"user":       map[string]string{"login": "reporter"},
		}))
	}))
	client := issueAuthorPermissionTestClient(t, server)

	authorization, permission, err := currentAuthorization(
		context.Background(),
		client,
		issueagentgithub.IssueFacts{
			Number: 42, Author: "reporter", AuthorAssociation: "CONTRIBUTOR",
		},
		nil,
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, authorization)
	require.Equal(t, "admin", permission)
	require.Equal(t, int32(2), attempts.Load())
}

func TestCurrentAuthorizationTreatsMissingIssueAuthorPermissionAsRead(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		attempts.Add(1)
		http.NotFound(writer, request)
	}))
	client := issueAuthorPermissionTestClient(t, server)

	authorization, permission, err := currentAuthorization(
		context.Background(),
		client,
		issueagentgithub.IssueFacts{
			Number: 42, Author: "reporter", AuthorAssociation: "CONTRIBUTOR",
		},
		nil,
		nil,
	)
	require.NoError(t, err)
	require.Nil(t, authorization)
	require.Equal(t, "read", permission)
	require.Equal(t, int32(1), attempts.Load())
}

func TestCurrentAuthorizationStopsPermissionRecoveryOnCancellation(
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
		http.Error(writer, "transient failure", http.StatusInternalServerError)
	}))
	client := issueAuthorPermissionTestClient(t, server)
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

func issueAuthorPermissionTestClient(
	t *testing.T,
	server *httptest.Server,
) *issueagentgithub.Client {
	t.Helper()
	t.Cleanup(server.Close)
	client, err := issueagentgithub.NewClient(
		issueagentgithub.ClientConfig{
			BaseURL: server.URL, Repository: "WuKongIM/WuKongIM",
			Token: "token", MaxPages: 2, MaxBodyBytes: 1 << 20,
		},
		server.Client(),
	)
	require.NoError(t, err)
	return client
}
