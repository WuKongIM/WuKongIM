package transfer

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDependencyBoundaryDoesNotReachLegacyClusterControl(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list deps failed: %v\n%s", err, out)
	}

	for _, importPath := range strings.Fields(string(out)) {
		if importPath == "github.com/WuKongIM/WuKongIM/pkg/cluster" ||
			strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/cluster/") ||
			importPath == "github.com/WuKongIM/WuKongIM/pkg/controller" ||
			strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/controller/") {
			t.Fatalf("pkg/db/transfer dependency closure still imports legacy cluster/control package %q", importPath)
		}
	}
}
