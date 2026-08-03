package reviewagentgithub_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	github "github.com/WuKongIM/WuKongIM/internal/infra/reviewagentgithub"
)

func TestAppTokenMinterUsesExactCompileTimeRoleProfiles(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	now := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	requested := make(chan map[string]string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet &&
			request.URL.Path == "/app" {
			require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
				"id": 1, "slug": "review-app",
			}))
			return
		}
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "/app/installations/2/access_tokens", request.URL.Path)
		var body struct {
			RepositoryIDs []int64           `json:"repository_ids"`
			Permissions   map[string]string `json:"permissions"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
		require.Equal(t, []int64{3}, body.RepositoryIDs)
		requested <- body.Permissions
		require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"token":                "installation-token",
			"expires_at":           now.Add(55 * time.Minute).Format(time.RFC3339),
			"permissions":          body.Permissions,
			"repository_selection": "selected",
			"repositories": []map[string]any{{
				"id": 3, "full_name": "WuKongIM/WuKongIM",
			}},
		}))
	}))
	t.Cleanup(server.Close)
	privatePEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	for role, want := range map[github.AppRole]map[string]string{
		github.AppRoleReviewPublisher: {
			"checks": "write", "contents": "write", "issues": "write",
			"metadata":      "read",
			"pull_requests": "write",
		},
		github.AppRoleStateWriter: {
			"contents": "write", "metadata": "read",
		},
	} {
		minter, err := github.NewAppTokenMinter(
			github.AppTokenConfig{
				BaseURL: server.URL, AppID: 1, InstallationID: 2,
				AppSlug:      "review-app",
				RepositoryID: 3, Repository: "WuKongIM/WuKongIM",
				PrivateKeyPEM: privatePEM, Role: role,
			},
			server.Client(),
			func() time.Time { return now },
		)
		require.NoError(t, err)
		token, err := minter.Mint(context.Background())
		require.NoError(t, err)
		require.Equal(t, "installation-token", token.Token)
		require.Equal(t, want, <-requested)
	}
}

func TestAppTokenMinterRejectsUnknownRoleAndRedactsResponse(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privatePEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		http.Error(writer, "installation-secret-token", http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	_, err = github.NewAppTokenMinter(
		github.AppTokenConfig{
			BaseURL: server.URL, AppID: 1, InstallationID: 2,
			AppSlug:      "review-app",
			RepositoryID: 3, Repository: "WuKongIM/WuKongIM",
			PrivateKeyPEM: privatePEM, Role: github.AppRole("custom"),
		},
		server.Client(),
		time.Now,
	)
	require.Error(t, err)

	minter, err := github.NewAppTokenMinter(
		github.AppTokenConfig{
			BaseURL: server.URL, AppID: 1, InstallationID: 2,
			AppSlug:      "review-app",
			RepositoryID: 3, Repository: "WuKongIM/WuKongIM",
			PrivateKeyPEM: privatePEM,
			Role:          github.AppRoleReviewPublisher,
		},
		server.Client(),
		time.Now,
	)
	require.NoError(t, err)
	_, err = minter.Mint(context.Background())
	require.Error(t, err)
	require.NotContains(t, err.Error(), "installation-secret-token")
	require.NotContains(t, err.Error(), string(privatePEM))
}

func TestAppTokenMinterRejectsUnexpectedAppIdentity(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privatePEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		require.Equal(t, http.MethodGet, request.Method)
		require.Equal(t, "/app", request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"id": 1, "slug": "unexpected-app",
		}))
	}))
	t.Cleanup(server.Close)

	minter, err := github.NewAppTokenMinter(
		github.AppTokenConfig{
			BaseURL: server.URL, AppID: 1, AppSlug: "review-app",
			InstallationID: 2, RepositoryID: 3,
			Repository:    "WuKongIM/WuKongIM",
			PrivateKeyPEM: privatePEM,
			Role:          github.AppRoleReviewPublisher,
		},
		server.Client(),
		time.Now,
	)
	require.NoError(t, err)
	_, err = minter.Mint(context.Background())
	require.EqualError(t, err, "GitHub App identity is inconsistent")
}
