package reviewagent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	repositoryPattern = regexp.MustCompile(
		`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`,
	)
	gitSHAPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	checkNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

func decodeStrictJSON(reader io.Reader, maxBytes int64, output any) error {
	if reader == nil || maxBytes <= 0 {
		return errors.New("JSON input limit must be positive")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return fmt.Errorf("read JSON input: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return errors.New("JSON input exceeds byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode JSON input: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("JSON input contains multiple values")
		}
		return fmt.Errorf("decode trailing JSON input: %w", err)
	}
	return nil
}

func canonicalDigest(value any, message string) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("%s: %w", message, err)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validRepository(repository string) bool {
	return len(repository) <= 256 &&
		repositoryPattern.MatchString(repository) &&
		!strings.Contains(repository, "..")
}

func validText(value string, maxBytes int, required bool) bool {
	if required && value == "" {
		return false
	}
	return len(value) <= maxBytes &&
		utf8.ValidString(value) &&
		!strings.ContainsRune(value, '\x00')
}

func validRepositoryPath(path string) bool {
	if !validText(path, 4096, true) ||
		strings.HasPrefix(path, "/") ||
		strings.HasSuffix(path, "/") ||
		strings.Contains(path, `\`) {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	return digestPattern.MatchString(value)
}

func validSHA(value string) bool {
	return gitSHAPattern.MatchString(value)
}

func validUniqueStrings(
	values []string,
	maxItems int,
	maxBytes int,
	requiredItems bool,
) bool {
	if len(values) > maxItems || requiredItems && len(values) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validText(value, maxBytes, true) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
