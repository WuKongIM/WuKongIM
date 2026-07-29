package issueagent_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
	"github.com/stretchr/testify/require"
)

func TestValidateTransitionAcceptsOnlyApprovedLifecycleEdges(t *testing.T) {
	t.Parallel()

	legal := map[issueagent.State][]issueagent.State{
		issueagent.StateAwaitingTriage: {
			issueagent.StateNeedsInfo,
			issueagent.StateAuthorized,
		},
		issueagent.StateNeedsInfo: {
			issueagent.StateAuthorized,
		},
		issueagent.StateAuthorized: {
			issueagent.StateVersionPinned,
		},
		issueagent.StateVersionPinned: {
			issueagent.StateReproducing,
		},
		issueagent.StateReproducing: {
			issueagent.StateNeedsInfo,
			issueagent.StateAlreadyFixed,
			issueagent.StateReproduced,
			issueagent.StateVersionPinned,
		},
		issueagent.StateReproduced: {
			issueagent.StateDraftPROpen,
		},
		issueagent.StateDraftPROpen: {
			issueagent.StateDiagnosing,
		},
		issueagent.StateDiagnosing: {
			issueagent.StateDiagnosed,
			issueagent.StateDraftPROpen,
			issueagent.StateReadyForHuman,
		},
		issueagent.StateDiagnosed: {
			issueagent.StateFixing,
			issueagent.StateReadyForHuman,
		},
		issueagent.StateFixing: {
			issueagent.StateValidating,
			issueagent.StateAlreadyFixed,
			issueagent.StateDiagnosed,
			issueagent.StateReadyForHuman,
		},
		issueagent.StateValidating: {
			issueagent.StateFixing,
			issueagent.StateAlreadyFixed,
			issueagent.StateReadyForReview,
			issueagent.StateReadyForHuman,
		},
		issueagent.StateReadyForReview: {
			issueagent.StateMerged,
			issueagent.StateReadyForHuman,
		},
	}

	for from, targets := range legal {
		from := from
		for _, to := range targets {
			to := to
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				t.Parallel()
				require.NoError(t, issueagentusecase.ValidateTransition(from, to))
			})
		}
	}

	require.Error(t, issueagentusecase.ValidateTransition(
		issueagent.StateAuthorized, issueagent.StateReadyForReview,
	))
	for _, terminal := range []issueagent.State{
		issueagent.StateAlreadyFixed,
		issueagent.StateMerged,
		issueagent.StateCancelled,
		issueagent.StateSuperseded,
		issueagent.StateWontFix,
	} {
		require.Error(t, issueagentusecase.ValidateTransition(
			terminal, issueagent.StateAuthorized,
		))
	}
}

func TestValidateTransitionAllowsExplicitHumanTerminalEdges(t *testing.T) {
	t.Parallel()

	for _, from := range []issueagent.State{
		issueagent.StateAuthorized,
		issueagent.StateVersionPinned,
		issueagent.StateReproducing,
		issueagent.StateReproduced,
		issueagent.StateDraftPROpen,
		issueagent.StateDiagnosing,
		issueagent.StateDiagnosed,
		issueagent.StateFixing,
		issueagent.StateValidating,
		issueagent.StateReadyForReview,
	} {
		require.NoError(t, issueagentusecase.ValidateTransition(
			from, issueagent.StateCancelled,
		))
		require.NoError(t, issueagentusecase.ValidateTransition(
			from, issueagent.StateSuperseded,
		))
		require.NoError(t, issueagentusecase.ValidateTransition(
			from, issueagent.StateWontFix,
		))
	}
}
