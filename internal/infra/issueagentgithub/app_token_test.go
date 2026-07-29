package issueagentgithub_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestAppTokenMinterRequestsExactRepositoryAndPermissions(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	const appID int64 = 1234
	const installationID int64 = 5678
	const repositoryID int64 = 9012
	repositorySelection := "selected"

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		require.Equal(t,
			"/app/installations/5678/access_tokens",
			request.URL.Path,
		)
		require.Equal(t, "application/vnd.github+json", request.Header.Get("Accept"))
		require.Equal(t, "2022-11-28", request.Header.Get("X-GitHub-Api-Version"))

		rawJWT := request.Header.Get("Authorization")
		require.Regexp(t, `^Bearer [^.]+\.[^.]+\.[^.]+$`, rawJWT)
		rawJWT = rawJWT[len("Bearer "):]
		claims := &jwt.RegisteredClaims{}
		parsed, err := jwt.ParseWithClaims(
			rawJWT,
			claims,
			func(*jwt.Token) (any, error) { return &privateKey.PublicKey, nil },
			jwt.WithValidMethods([]string{"RS256"}),
			jwt.WithTimeFunc(func() time.Time { return now }),
		)
		require.NoError(t, err)
		require.True(t, parsed.Valid)
		require.Equal(t, strconv.FormatInt(appID, 10), claims.Issuer)

		var body struct {
			RepositoryIDs []int64           `json:"repository_ids"`
			Permissions   map[string]string `json:"permissions"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
		require.Equal(t, []int64{repositoryID}, body.RepositoryIDs)
		require.Equal(t, map[string]string{
			"actions":       "write",
			"contents":      "write",
			"issues":        "write",
			"metadata":      "read",
			"pull_requests": "write",
		}, body.Permissions)

		writer.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(writer).Encode(map[string]any{
			"token":                "installation-secret-token",
			"expires_at":           now.Add(55 * time.Minute).Format(time.RFC3339),
			"permissions":          body.Permissions,
			"repository_selection": repositorySelection,
			"repositories": []map[string]any{{
				"id": repositoryID, "full_name": "WuKongIM/WuKongIM",
				"private": false,
			}},
		}))
	}))
	t.Cleanup(server.Close)

	privatePEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
	minter, err := issueagentgithub.NewAppTokenMinter(
		issueagentgithub.AppTokenConfig{
			BaseURL:        server.URL,
			AppID:          appID,
			InstallationID: installationID,
			RepositoryID:   repositoryID,
			Repository:     "WuKongIM/WuKongIM",
			PrivateKeyPEM:  privatePEM,
		},
		server.Client(),
		func() time.Time { return now },
	)
	require.NoError(t, err)

	token, err := minter.Mint(context.Background())
	require.NoError(t, err)
	require.Equal(t, "installation-secret-token", token.Token)
	require.Equal(t, now.Add(55*time.Minute), token.ExpiresAt)

	repositorySelection = "all"
	_, err = minter.Mint(context.Background())
	require.Error(t, err)
}

func TestAppTokenMinterRedactsSecretsFromErrors(t *testing.T) {
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

	minter, err := issueagentgithub.NewAppTokenMinter(
		issueagentgithub.AppTokenConfig{
			BaseURL:        server.URL,
			AppID:          1,
			InstallationID: 2,
			RepositoryID:   3,
			Repository:     "WuKongIM/WuKongIM",
			PrivateKeyPEM:  privatePEM,
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
