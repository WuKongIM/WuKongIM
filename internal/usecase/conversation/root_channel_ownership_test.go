package conversation

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConversationUsecaseSourcesUseRootChannelTypes(t *testing.T) {
	files := []string{
		"deps.go",
		"types.go",
		"projector.go",
		"sync.go",
	}
	for _, path := range files {
		body, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NotContains(t, string(body), "pkg/channel/log", path)
		require.Contains(t, string(body), "pkg/channel", path)
	}
}
