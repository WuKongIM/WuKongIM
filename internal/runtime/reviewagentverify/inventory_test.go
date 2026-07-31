package reviewagentverify_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

func TestInventoryRequiresEveryDeclaredFileAndRenameIdentity(t *testing.T) {
	t.Parallel()

	inventory, err := verify.BuildInventory(
		2,
		[]verify.RawFile{
			{
				Path:    "internal/app/new.go",
				OldPath: "internal/app/old.go",
				Status:  contract.FileStatusRenamed,
				Mode:    "100644",
				Type:    verify.FileTypeText,
				Patch:   []byte("@@ rename @@"),
				Content: []byte("package app\n"),
			},
			{
				Path:      "web/public/logo.png",
				Status:    contract.FileStatusModified,
				Mode:      "100644",
				Type:      verify.FileTypeBinary,
				Content:   []byte{0x89, 'P', 'N', 'G'},
				Generated: true,
			},
		},
		verify.InventoryLimits{
			MaxFiles:      10,
			MaxTotalBytes: 1 << 20,
			MaxLines:      1000,
		},
	)
	require.NoError(t, err)
	require.True(t, inventory.Complete)
	require.Len(t, inventory.Files, 2)
	require.Equal(
		t,
		"internal/app/old.go",
		inventory.Files[0].PreviousPath,
	)
	require.Equal(t, "binary", inventory.Files[1].Type)
	require.True(t, inventory.Files[1].Generated)
	require.NotEmpty(t, inventory.Files[1].ContentDigest)
}

func TestInventoryFailsClosedOnTruncationOrCountMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		declared int
		files    []verify.RawFile
	}{
		{
			name:     "count mismatch",
			declared: 2,
			files: []verify.RawFile{{
				Path: "README.md", Status: contract.FileStatusModified,
				Mode: "100644", Type: verify.FileTypeText,
				Patch: []byte("patch"), Content: []byte("readme"),
			}},
		},
		{
			name:     "truncated patch",
			declared: 1,
			files: []verify.RawFile{{
				Path: "README.md", Status: contract.FileStatusModified,
				Mode: "100644", Type: verify.FileTypeText,
				Patch: []byte("patch"), Content: []byte("readme"),
				PatchTruncated: true,
			}},
		},
		{
			name:     "rename lacks old path",
			declared: 1,
			files: []verify.RawFile{{
				Path: "README.md", Status: contract.FileStatusRenamed,
				Mode: "100644", Type: verify.FileTypeText,
				Patch: []byte("patch"), Content: []byte("readme"),
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := verify.BuildInventory(
				test.declared,
				test.files,
				verify.InventoryLimits{
					MaxFiles:      10,
					MaxTotalBytes: 1 << 20,
					MaxLines:      1000,
				},
			)
			require.Error(t, err)
		})
	}

	inventory, err := verify.BuildInventory(
		1,
		[]verify.RawFile{{
			Path: "README.md", Status: contract.FileStatusModified,
			Mode: "100644", Type: verify.FileTypeText,
			Patch:   []byte(strings.Repeat("x", 100)),
			Content: []byte(strings.Repeat("x", 100)),
		}},
		verify.InventoryLimits{
			MaxFiles: 10, MaxTotalBytes: 100, MaxLines: 1,
		},
	)
	require.NoError(t, err)
	require.Equal(t, int64(100), inventory.TotalBytes)
	require.Equal(t, int64(1), inventory.TotalLines)

	_, err = verify.BuildInventory(
		1,
		[]verify.RawFile{{
			Path: "README.md", Status: contract.FileStatusModified,
			Mode: "100644", Type: verify.FileTypeText,
			Patch:   []byte(strings.Repeat("x", 101)),
			Content: []byte(strings.Repeat("x", 100)),
		}},
		verify.InventoryLimits{
			MaxFiles: 10, MaxTotalBytes: 100, MaxLines: 1,
		},
	)
	require.EqualError(t, err, "changed-byte budget exceeded")
}
