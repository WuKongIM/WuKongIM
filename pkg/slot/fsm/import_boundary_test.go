package fsm

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDependencyBoundaryDoesNotReachLegacyClusterHashSlot(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list deps failed: %v\n%s", err, out)
	}

	for _, importPath := range strings.Fields(string(out)) {
		if importPath == "github.com/WuKongIM/WuKongIM/pkg/cluster/hashslot" {
			t.Fatalf("pkg/slot/fsm dependency closure still imports legacy cluster hashslot package %q", importPath)
		}
	}
}
