package reviewagentgithub

import (
	"testing"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

func TestReviewInventoryLimits(t *testing.T) {
	t.Parallel()

	limits := reviewInventoryLimits()
	if limits.MaxFiles != contract.MaxChangedFiles ||
		limits.MaxTotalBytes != contract.MaxChangedBytes ||
		limits.MaxLines != contract.MaxChangedLines {
		t.Fatalf("review inventory limits = %+v", limits)
	}
}

func TestReviewInventoryBudgetFailure(t *testing.T) {
	t.Parallel()

	limits := reviewInventoryLimits()
	if reason := reviewInventoryBudgetFailure(verify.Inventory{
		TotalBytes: limits.MaxTotalBytes,
		TotalLines: limits.MaxLines,
	}, limits); reason != "" {
		t.Fatalf("exact budget rejected: %s", reason)
	}
	if reason := reviewInventoryBudgetFailure(verify.Inventory{
		TotalBytes: limits.MaxTotalBytes + 1,
	}, limits); reason != "changed-byte budget exceeded" {
		t.Fatalf("byte overflow reason = %q", reason)
	}
	if reason := reviewInventoryBudgetFailure(verify.Inventory{
		TotalLines: limits.MaxLines + 1,
	}, limits); reason != "changed-line budget exceeded" {
		t.Fatalf("line overflow reason = %q", reason)
	}
}
