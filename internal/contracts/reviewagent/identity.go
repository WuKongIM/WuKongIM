package reviewagent

import (
	"errors"
	"sort"
	"strings"
)

const (
	maxIntentTitleBytes = 1024
	maxIntentBodyBytes  = 64 << 10
	maxIntentLinks      = 64
)

// GenerationIdentity binds every Review Agent document to one immutable pull
// request generation and its exact signed state parent.
type GenerationIdentity struct {
	Repository     string `json:"repository"`
	PullRequest    int64  `json:"pull_request"`
	HeadSHA        string `json:"head_sha"`
	BaseSHA        string `json:"base_sha"`
	TestMergeSHA   string `json:"test_merge_sha"`
	IntentDigest   string `json:"intent_digest"`
	Generation     uint64 `json:"generation"`
	StateParentSHA string `json:"state_parent_sha"`
}

// ValidateGenerationIdentity rejects partial or ambiguous generation
// coordinates.
func ValidateGenerationIdentity(identity GenerationIdentity) error {
	if !validRepository(identity.Repository) || identity.PullRequest <= 0 {
		return errors.New("invalid Review generation repository or pull request")
	}
	if !validSHA(identity.HeadSHA) ||
		!validSHA(identity.BaseSHA) ||
		!validSHA(identity.TestMergeSHA) ||
		!validSHA(identity.StateParentSHA) {
		return errors.New("invalid Review generation Git identity")
	}
	if !validDigest(identity.IntentDigest) || identity.Generation == 0 {
		return errors.New("invalid Review generation semantic identity")
	}
	return nil
}

// GenerationIdentityDigest identifies all immutable coordinates of one
// generation.
func GenerationIdentityDigest(identity GenerationIdentity) (string, error) {
	if err := ValidateGenerationIdentity(identity); err != nil {
		return "", err
	}
	return canonicalDigest(identity, "encode Review generation identity")
}

// MustGenerationDigest is intended for already-validated internal documents.
func MustGenerationDigest(identity GenerationIdentity) string {
	digest, err := GenerationIdentityDigest(identity)
	if err != nil {
		panic(err)
	}
	return digest
}

// IntentDigest canonicalizes human intent without interpreting it.
func IntentDigest(title, body string, linkedSpecifications []string) (string, error) {
	title = normalizeIntentText(title)
	body = normalizeIntentText(body)
	if !validText(title, maxIntentTitleBytes, true) ||
		!validText(body, maxIntentBodyBytes, false) ||
		!validUniqueStrings(
			linkedSpecifications,
			maxIntentLinks,
			1024,
			false,
		) {
		return "", errors.New("invalid Review intent")
	}
	links := append([]string(nil), linkedSpecifications...)
	sort.Strings(links)
	return canonicalDigest(struct {
		Title                string   `json:"title"`
		Body                 string   `json:"body"`
		LinkedSpecifications []string `json:"linked_specifications"`
	}{
		Title:                title,
		Body:                 body,
		LinkedSpecifications: links,
	}, "encode Review intent")
}

func normalizeIntentText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(value, "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
