package flowdoc

import (
	"errors"
	"fmt"
	"strings"
)

var requiredHeadings = []string{
	"## Responsibility",
	"## Boundaries",
	"## Main Flows",
	"## Invariants and Failure Semantics",
	"## Read First",
	"## Update Triggers",
}

// ValidateBody checks the stable navigation-card outline. Semantic quality
// remains a review responsibility.
func ValidateBody(content []byte) error {
	lines, closing, err := splitFrontMatter(content)
	if err != nil {
		return err
	}

	position := closing + 1
	for position < len(lines) && lines[position] == "" {
		position++
	}
	if position == len(lines) ||
		!strings.HasPrefix(lines[position], "# ") ||
		!strings.HasSuffix(lines[position], " Flow") {
		return errors.New("FLOW title must be '# <module> Flow'")
	}
	position++
	headingPositions := make([]int, 0, len(requiredHeadings))
	expected := 0
	inFence := false
	for ; position < len(lines); position++ {
		line := lines[position]
		if markdownFence(line) {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(line, "## ") {
			continue
		}
		if expected == len(requiredHeadings) {
			return fmt.Errorf("unexpected second-level heading %q", line)
		}
		if line != requiredHeadings[expected] {
			return fmt.Errorf(
				"required heading %q is missing or out of order",
				requiredHeadings[expected],
			)
		}
		headingPositions = append(headingPositions, position)
		expected++
	}
	if expected != len(requiredHeadings) {
		return fmt.Errorf(
			"required heading %q is missing or out of order",
			requiredHeadings[expected],
		)
	}
	for index, headingPosition := range headingPositions {
		end := len(lines)
		if index+1 < len(headingPositions) {
			end = headingPositions[index+1]
		}
		if !sectionHasContent(lines[headingPosition+1 : end]) {
			return fmt.Errorf("section %q must not be empty", requiredHeadings[index])
		}
	}
	return nil
}

func markdownFence(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func sectionHasContent(lines []string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || markdownFence(trimmed) || strings.HasPrefix(trimmed, "### ") {
			continue
		}
		return true
	}
	return false
}
