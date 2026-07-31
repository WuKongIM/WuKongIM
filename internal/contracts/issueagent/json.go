package issueagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var (
	repositoryPattern = regexp.MustCompile(
		`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`,
	)
	gitSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func decodeStrictJSON(reader io.Reader, maxBytes int64, output any) error {
	body, err := readBoundedJSON(reader, maxBytes)
	if err != nil {
		return err
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

func readBoundedJSON(reader io.Reader, maxBytes int64) ([]byte, error) {
	if reader == nil || maxBytes <= 0 {
		return nil, errors.New("JSON input limit must be positive")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read JSON input: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, errors.New("JSON input exceeds byte limit")
	}
	return body, nil
}

func validRepository(repository string) bool {
	return len(repository) > 0 &&
		len(repository) <= 256 &&
		repositoryPattern.MatchString(repository) &&
		!strings.Contains(repository, "..")
}
