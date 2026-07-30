package issueagent

import (
	"strings"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

const (
	bugTitlePrefix = "[BUG]"
	bugLabel       = "bug"
)

var requiredBugSections = []string{
	"Environment, topology, and client",
	"Reproduction steps",
	"Expected and actual result",
}

// AssessBugIssue validates the protected Bug intake shape.
func AssessBugIssue(
	title string,
	body string,
	labels []string,
	versionReason string,
) (bool, string) {
	if !strings.HasPrefix(strings.TrimSpace(title), bugTitlePrefix) {
		return false, "Issue must use the Bug template title prefix [BUG]."
	}
	if !containsFold(labels, bugLabel) {
		return false, "Issue must carry the Bug template label."
	}
	for _, section := range requiredBugSections {
		value, ambiguous := issueFormValue(body, section, 16<<10)
		if ambiguous || value == "" || value == "_No response_" {
			return false, "Complete the Bug template section: " + section + "."
		}
	}
	if versionReason != "" {
		return false, versionReason
	}
	return true, ""
}

// IssueFormValue reads one exact GitHub Issue form section.
func IssueFormValue(body string, label string) (string, bool) {
	return issueFormValue(body, label, 512)
}

func issueFormValue(body string, label string, maxBytes int) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	marker := "### " + label
	value := ""
	found := false
	for index := 0; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) != marker {
			continue
		}
		if found {
			return "", true
		}
		found = true
		var section []string
		for index++; index < len(lines); index++ {
			if strings.HasPrefix(strings.TrimSpace(lines[index]), "### ") {
				index--
				break
			}
			section = append(section, lines[index])
		}
		value = strings.TrimSpace(strings.Join(section, "\n"))
	}
	if len(value) > maxBytes || strings.ContainsRune(value, '\x00') {
		return "", true
	}
	return value, false
}

// ClassifyIssueRisk maps protected topic policy onto the admission risk.
func ClassifyIssueRisk(
	title string,
	body string,
	labels []string,
	highRiskTopics []string,
) contract.CandidateRisk {
	text := strings.ToLower(title + "\n" + body + "\n" +
		strings.Join(labels, "\n"))
	for _, topic := range highRiskTopics {
		if strings.Contains(text, strings.ToLower(topic)) {
			return contract.CandidateRiskInvestigation
		}
	}
	return contract.CandidateRiskLow
}

// TrustedAssociation reports whether GitHub identifies a repository insider.
func TrustedAssociation(association string) bool {
	switch association {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	default:
		return false
	}
}

// WritePermission reports whether a current GitHub permission may authorize.
func WritePermission(permission string) bool {
	switch permission {
	case "write", "maintain", "admin":
		return true
	default:
		return false
	}
}

// TracksIssueState reports whether the Controller sweep must retain the Issue.
func TracksIssueState(state contract.IssueState) bool {
	switch state {
	case contract.IssueStateEngineering,
		contract.IssueStateReviewing,
		contract.IssueStateDraft,
		contract.IssueStateReadyForReview:
		return true
	default:
		return false
	}
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}
