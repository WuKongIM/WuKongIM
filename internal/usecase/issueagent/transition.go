package issueagent

import issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"

// ValidateTransition enforces the approved lifecycle graph for one generation.
func ValidateTransition(from, to issueagentcontract.State) error {
	return issueagentcontract.ValidateTransition(from, to)
}
