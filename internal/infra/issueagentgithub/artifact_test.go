package issueagentgithub_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

func TestArtifactDownloadRequiresExactMetadataAndDigest(t *testing.T) {
	t.Parallel()

	content := []byte("bounded artifact")
	sum := sha256.Sum256(content)
	expectedDigest := "sha256:" + hex.EncodeToString(sum[:])
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "Bearer token", request.Header.Get("Authorization"))
		writer.Header().Set("Content-Type", "application/zip")
		_, _ = writer.Write(content)
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	artifact := issueagentgithub.ArtifactFacts{
		ID: 12, Name: "issue-agent-result", SizeInBytes: int64(len(content)),
		DownloadURL: server.URL + "/download",
	}

	downloaded, err := client.DownloadArtifact(
		context.Background(), artifact, 12, "issue-agent-result",
		expectedDigest, 1024,
	)
	require.NoError(t, err)
	require.Equal(t, content, downloaded)

	_, err = client.DownloadArtifact(
		context.Background(), artifact, 12, "issue-agent-result",
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		1024,
	)
	require.Error(t, err)
}

func TestArtifactDownloadRejectsExpiredOversizedAndCrossHost(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/zip")
		_, _ = writer.Write(make([]byte, 128))
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server)
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, artifact := range []issueagentgithub.ArtifactFacts{
		{
			ID: 1, Name: "result", SizeInBytes: 128,
			Expired: true, DownloadURL: server.URL,
		},
		{
			ID: 1, Name: "result", SizeInBytes: 128,
			DownloadURL: "https://attacker.invalid/result",
		},
		{
			ID: 1, Name: "result", SizeInBytes: 2048,
			DownloadURL: server.URL,
		},
	} {
		_, err := client.DownloadArtifact(
			context.Background(), artifact, 1, "result", digest, 1024,
		)
		require.Error(t, err)
	}
}
