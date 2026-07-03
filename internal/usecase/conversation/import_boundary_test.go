package conversation

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestConversationUsecaseImportBoundary(t *testing.T) {
	forbidden := []string{
		"github.com/WuKongIM/WuKongIM/pkg/gateway",
		"github.com/WuKongIM/WuKongIM/pkg/protocol/frame",
		"github.com/WuKongIM/WuKongIM/pkg/cluster",
		"github.com/WuKongIM/WuKongIM/pkg/channel",
		"github.com/WuKongIM/WuKongIM/internal/access",
		"github.com/WuKongIM/WuKongIM/internal/app",
		"github.com/WuKongIM/WuKongIM/internal/usecase",
	}
	files, err := parser.ParseDir(token.NewFileSet(), ".", func(info os.FileInfo) bool {
		name := info.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("ParseDir() error = %v", err)
	}
	for _, pkg := range files {
		for filename, file := range pkg.Files {
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				for _, bad := range forbidden {
					if path == bad || strings.HasPrefix(path, bad+"/") {
						t.Fatalf("%s imports forbidden package %q", filename, path)
					}
				}
			}
		}
	}
}
