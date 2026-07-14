package alibaba

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCloudInitConfiguresOnlyProviderAssignedSecondaryAddresses(t *testing.T) {
	content := cloudInit("ssh-ed25519 AAAATEST run", []string{"10.42.0.21", "10.42.0.22"}, 24)
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		t.Fatalf("cloud-init YAML error = %v:\n%s", err, content)
	}
	for _, expected := range []string{"ssh_pwauth: false", "ip address replace 10.42.0.21/24", "ip address replace 10.42.0.22/24"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("cloud-init missing %q:\n%s", expected, content)
		}
	}
	if strings.Contains(content, "10.42.0.20/24") {
		t.Fatalf("cloud-init must not replace the primary address:\n%s", content)
	}
}

func TestClientTokenKeepsLongRunSuffixesDistinct(t *testing.T) {
	runID := strings.Repeat("shared-prefix-", 20)
	first := clientToken(runID, "node-1")
	second := clientToken(runID, "node-2")
	if first == second || len(first) != 64 || len(second) != 64 {
		t.Fatalf("client tokens = %q / %q, want distinct 64-character digests", first, second)
	}
}

func TestCollectPagesRequiresCompleteProviderInventory(t *testing.T) {
	pages := map[int32][]int{1: {1, 2}, 2: {3}}
	values, err := collectPages(context.Background(), func(page int32) ([]int, int, error) {
		return pages[page], 3, nil
	})
	if err != nil || len(values) != 3 {
		t.Fatalf("collectPages() = %v, %v, want three complete values", values, err)
	}
	_, err = collectPages(context.Background(), func(page int32) ([]int, int, error) {
		if page == 1 {
			return []int{1}, 2, nil
		}
		return nil, 2, nil
	})
	if !errors.Is(err, ErrAmbiguousInventory) {
		t.Fatalf("incomplete inventory error = %v, want ErrAmbiguousInventory", err)
	}
}

func TestAttachableInstanceStatusMatchesAlibabaAttachDiskContract(t *testing.T) {
	for _, status := range []string{"Running", "Stopped"} {
		if !attachableInstanceStatus(status) {
			t.Fatalf("status %q should be attachable", status)
		}
	}
	for _, status := range []string{"", "Pending", "Starting", "Stopping"} {
		if attachableInstanceStatus(status) {
			t.Fatalf("status %q must not be attachable", status)
		}
	}
}
