package control

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionImportsOnlyControllerV2Facade(t *testing.T) {
	disallowed := []string{
		"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command",
		"github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm",
		"github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft",
		"github.com/WuKongIM/WuKongIM/pkg/controllerv2/server",
		"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state",
		"github.com/WuKongIM/WuKongIM/pkg/controllerv2/statefile",
		"github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("ParseFile(%s) error = %v", file, err)
		}
		for _, spec := range parsed.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("Unquote(%s) error = %v", spec.Path.Value, err)
			}
			for _, blocked := range disallowed {
				if path == blocked {
					wd, _ := os.Getwd()
					t.Fatalf("%s imports ControllerV2 implementation package %q; production code should import only root pkg/controllerv2 from %s", file, path, wd)
				}
			}
		}
	}
}
