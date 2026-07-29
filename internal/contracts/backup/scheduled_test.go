package backup

import "testing"

func TestSystemStateCloneDetachesRepositoryVerification(t *testing.T) {
	testCases := []struct {
		name         string
		verification *RepositoryVerification
	}{
		{name: "legacy"},
		{
			name: "unverified",
			verification: &RepositoryVerification{
				Status: RepositoryVerificationUnverified,
			},
		},
		{
			name: "verified",
			verification: &RepositoryVerification{
				Status:               RepositoryVerificationVerified,
				VerifiedAtUnixMillis: 1_800_000_000_500,
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			state := SystemState{
				Plan: &Plan{
					Revision:               1,
					RepositoryVerification: testCase.verification,
				},
			}

			cloned := state.Clone()
			if testCase.verification == nil {
				if cloned.Plan.RepositoryVerification != nil {
					t.Fatalf(
						"legacy verification = %#v",
						cloned.Plan.RepositoryVerification,
					)
				}
				return
			}
			if cloned.Plan.RepositoryVerification == testCase.verification {
				t.Fatal("repository verification pointer was aliased")
			}
			if *cloned.Plan.RepositoryVerification != *testCase.verification {
				t.Fatalf(
					"verification = %#v, want %#v",
					cloned.Plan.RepositoryVerification,
					testCase.verification,
				)
			}
			cloned.Plan.RepositoryVerification.Status =
				RepositoryVerificationUnverified
			if testCase.verification.Status ==
				RepositoryVerificationVerified &&
				state.Plan.RepositoryVerification.Status !=
					RepositoryVerificationVerified {
				t.Fatal("mutating clone changed source verification")
			}
		})
	}
}
