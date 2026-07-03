package message_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDependencyBoundaryDoesNotReachChannelRuntime(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list deps failed: %v\n%s", err, out)
	}

	for _, importPath := range strings.Fields(string(out)) {
		switch {
		case importPath == "github.com/WuKongIM/WuKongIM/pkg/channel" ||
			strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/channel/"):
			t.Fatalf("pkg/db/message dependency closure still imports channel runtime package %q", importPath)
		case importPath == "github.com/WuKongIM/WuKongIM/pkg/channel" ||
			strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/channel/"):
			t.Fatalf("pkg/db/message dependency closure still imports v2 channel runtime package %q", importPath)
		case importPath == "github.com/WuKongIM/WuKongIM/pkg/legacy/channel" ||
			strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/legacy/channel/"):
			t.Fatalf("pkg/db/message dependency closure still imports legacy channel runtime package %q", importPath)
		}
	}
}
