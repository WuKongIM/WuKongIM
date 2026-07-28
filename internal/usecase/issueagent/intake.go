package issueagent

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const (
	maxIssueBodyBytes   = 64 << 10
	maxIntakeMessageLen = 4096
	maxDuplicateLinks   = 5
)

var (
	fullCommitPattern = regexp.MustCompile(`\A[0-9a-fA-F]{40}\z`)
	semverTagPattern  = regexp.MustCompile(
		`\Av?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)` +
			`(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?` +
			`(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?\z`,
	)
	imageDigestPattern = regexp.MustCompile(
		`\A[a-zA-Z0-9][a-zA-Z0-9._/-]*(?::[a-zA-Z0-9][a-zA-Z0-9._-]*)?` +
			`@sha256:[0-9a-fA-F]{64}\z`,
	)
)

var bugFormHeadings = []struct {
	heading string
	assign  func(*BugForm, string)
}{
	{"Affected version", func(form *BugForm, value string) { form.AffectedVersion = value }},
	{"Environment, topology, and client", func(form *BugForm, value string) {
		form.Environment = value
	}},
	{"Reproduction steps", func(form *BugForm, value string) { form.Reproduction = value }},
	{"Expected and actual result", func(form *BugForm, value string) {
		form.ExpectedActual = value
	}},
	{"Frequency", func(form *BugForm, value string) { form.Frequency = value }},
	{"Logs or configuration", func(form *BugForm, value string) { form.Diagnostics = value }},
}

// BugForm is the bounded, deterministic projection of a rendered Bug Issue Form.
type BugForm struct {
	AffectedVersion string
	Environment     string
	Reproduction    string
	ExpectedActual  string
	Frequency       string
	Diagnostics     string
}

// IntakePlan describes the only actions available before maintainer authorization.
type IntakePlan struct {
	Form           BugForm
	Complete       bool
	Missing        []string
	Labels         []string
	Message        string
	InvokeModel    bool
	ResolveVersion bool
	CreateBranch   bool
}

// PlanIntake validates Bug Issue Form syntax without executing or resolving user input.
func PlanIntake(body string, possibleDuplicates []string) (IntakePlan, error) {
	if len(body) > maxIssueBodyBytes {
		return IntakePlan{}, errors.New("Issue body exceeds intake limit")
	}
	form, err := parseBugForm(body)
	if err != nil {
		return IntakePlan{}, err
	}
	missing := requiredBugFacts(form)
	plan := IntakePlan{
		Form:     form,
		Complete: len(missing) == 0,
		Missing:  missing,
		Labels:   []string{"needs-triage"},
	}
	if plan.Complete {
		return plan, nil
	}
	plan.Labels = []string{"needs-info"}
	plan.Message = intakeRequest(missing, possibleDuplicates)
	return plan, nil
}

func parseBugForm(body string) (BugForm, error) {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	sections := make(map[string]string, len(bugFormHeadings))
	current := ""
	var content []string
	flush := func() error {
		if current == "" {
			return nil
		}
		if _, duplicate := sections[current]; duplicate {
			return fmt.Errorf("Bug form heading %q occurs more than once", current)
		}
		sections[current] = cleanFormValue(strings.Join(content, "\n"))
		return nil
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "### ") {
			heading := strings.TrimSpace(strings.TrimPrefix(line, "### "))
			if isBugFormHeading(heading) {
				if err := flush(); err != nil {
					return BugForm{}, err
				}
				current = heading
				content = content[:0]
				continue
			}
		}
		if current != "" {
			content = append(content, line)
		}
	}
	if err := flush(); err != nil {
		return BugForm{}, err
	}

	var form BugForm
	for _, field := range bugFormHeadings {
		field.assign(&form, sections[field.heading])
	}
	return form, nil
}

func isBugFormHeading(candidate string) bool {
	for _, field := range bugFormHeadings {
		if field.heading == candidate {
			return true
		}
	}
	return false
}

func cleanFormValue(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "_no response_", "no response", "n/a", "none":
		return ""
	default:
		return value
	}
}

func requiredBugFacts(form BugForm) []string {
	var missing []string
	if !validImmutableVersionSyntax(form.AffectedVersion) {
		missing = append(missing, "affected version")
	}
	if form.Environment == "" {
		missing = append(missing, "environment, topology, and client")
	}
	if form.Reproduction == "" {
		missing = append(missing, "reproduction steps")
	}
	if form.ExpectedActual == "" {
		missing = append(missing, "expected and actual result")
	}
	return missing
}

func validImmutableVersionSyntax(version string) bool {
	version = strings.TrimSpace(version)
	if len(version) == 0 || len(version) > 256 {
		return false
	}
	return fullCommitPattern.MatchString(version) ||
		semverTagPattern.MatchString(version) ||
		imageDigestPattern.MatchString(version)
}

func intakeRequest(missing, candidates []string) string {
	var builder strings.Builder
	builder.WriteString("Thanks for the report. Before a maintainer can authorize the Issue Agent, please update: ")
	builder.WriteString(strings.Join(missing, "; "))
	builder.WriteString(". Use an exact release tag, full commit SHA, or image digest for the affected version.")
	duplicates := validDuplicateLinks(candidates)
	if len(duplicates) > 0 {
		builder.WriteString("\n\npossible duplicate")
		if len(duplicates) > 1 {
			builder.WriteByte('s')
		}
		builder.WriteString(" (advisory only): ")
		builder.WriteString(strings.Join(duplicates, ", "))
	}
	message := builder.String()
	if len(message) > maxIntakeMessageLen {
		return message[:maxIntakeMessageLen]
	}
	return message
}

func validDuplicateLinks(candidates []string) []string {
	result := make([]string, 0, min(len(candidates), maxDuplicateLinks))
	for _, candidate := range candidates {
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" ||
			!strings.Contains(parsed.Path, "/issues/") || parsed.RawQuery != "" ||
			parsed.Fragment != "" {
			continue
		}
		canonical := parsed.String()
		if slices.Contains(result, canonical) {
			continue
		}
		result = append(result, canonical)
		if len(result) == maxDuplicateLinks {
			break
		}
	}
	return result
}
