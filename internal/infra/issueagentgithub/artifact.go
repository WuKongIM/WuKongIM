package issueagentgithub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
)

// DownloadArtifact downloads opaque bytes only after exact metadata fencing.
func (client *Client) DownloadArtifact(
	ctx context.Context,
	artifact ArtifactFacts,
	expectedID int64,
	expectedName string,
	expectedSHA256 string,
	maxBytes int64,
) ([]byte, error) {
	if client == nil || expectedID <= 0 || expectedName == "" ||
		artifact.ID != expectedID || artifact.Name != expectedName ||
		artifact.Expired || artifact.SizeInBytes < 0 ||
		artifact.SizeInBytes > maxBytes || maxBytes <= 0 ||
		!digestPattern.MatchString(expectedSHA256) {
		return nil, errors.New("Artifact identity or bounds are invalid")
	}
	endpoint, err := url.Parse(artifact.DownloadURL)
	if err != nil || endpoint.Scheme != client.baseURL.Scheme ||
		endpoint.Host != client.baseURL.Host || endpoint.User != nil {
		return nil, errors.New("Artifact download URL is outside GitHub API scope")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, errors.New("create Artifact request")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("Authorization", "Bearer "+client.token)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, errors.New("Artifact download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, errors.New("Artifact download returned an unexpected status")
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/zip" {
		return nil, errors.New("Artifact response is not a ZIP archive")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, errors.New("read Artifact response")
	}
	if int64(len(body)) > maxBytes || int64(len(body)) != artifact.SizeInBytes {
		return nil, errors.New("Artifact response size does not match metadata")
	}
	sum := sha256.Sum256(body)
	actual := "sha256:" + hex.EncodeToString(sum[:])
	if actual != expectedSHA256 {
		return nil, errors.New("Artifact digest does not match expected result")
	}
	return body, nil
}

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
