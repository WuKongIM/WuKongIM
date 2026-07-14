package cloudsim

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestTencentAdmissionRequiresEveryRealAlibabaDrill(t *testing.T) {
	attestation := AlibabaCanaryAttestation{
		ReviewedAt: time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC), Reviewer: "repo-owner",
		Drills: make(map[string]CanaryDrill),
	}
	for index, name := range RequiredAlibabaCanaryDrills() {
		attestation.Drills[name] = CanaryDrill{
			WorkflowURL: fmt.Sprintf("https://github.com/WuKongIM/WuKongIM/actions/runs/%d", index+1),
			RunID:       fmt.Sprintf("gh-%d-1", index+1), Green: true, ZeroResidual: true,
		}
	}
	if err := ValidateTencentAdmission(attestation, "WuKongIM/WuKongIM"); err != nil {
		t.Fatalf("ValidateTencentAdmission() error = %v", err)
	}
	drill := attestation.Drills["detached-disk-cleanup"]
	drill.ZeroResidual = false
	attestation.Drills["detached-disk-cleanup"] = drill
	if err := ValidateTencentAdmission(attestation, "WuKongIM/WuKongIM"); !errors.Is(err, ErrAlibabaCanaryIncomplete) {
		t.Fatalf("incomplete admission error = %v", err)
	}
}
