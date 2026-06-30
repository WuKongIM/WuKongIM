package nodeops

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestNodeOpsDoesNotImportClusterInternals(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps . failed: %v\n%s", err, output)
	}

	for _, dep := range strings.Fields(string(output)) {
		for _, forbidden := range []string{
			"github.com/WuKongIM/WuKongIM/internalv2/",
			"github.com/WuKongIM/WuKongIM/pkg/controllerv2",
			"github.com/WuKongIM/WuKongIM/pkg/clusterv2",
			"github.com/WuKongIM/WuKongIM/pkg/db/",
		} {
			if strings.HasPrefix(dep, forbidden) {
				t.Fatalf("nodeops imports forbidden dependency %q", dep)
			}
		}
	}
}
