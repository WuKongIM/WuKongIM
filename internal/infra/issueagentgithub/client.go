package issueagentgithub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var repositoryNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// ClientConfig bounds one repository-specific GitHub REST client.
type ClientConfig struct {
	BaseURL      string
	Repository   string
	Token        string
	MaxPages     int
	MaxBodyBytes int64
}

// Client performs bounded strict GitHub API reads and writes.
type Client struct {
	baseURL      *url.URL
	repository   string
	token        string
	maxPages     int
	maxBodyBytes int64
	httpClient   *http.Client
}

// NewClient constructs a client that rejects redirects and cross-host pages.
func NewClient(config ClientConfig, httpClient *http.Client) (*Client, error) {
	if !repositoryNamePattern.MatchString(config.Repository) ||
		strings.Contains(config.Repository, "..") ||
		config.Token == "" ||
		len(config.Token) > 4096 ||
		strings.ContainsAny(config.Token, "\r\n") ||
		config.MaxPages <= 0 ||
		config.MaxPages > 100 ||
		config.MaxBodyBytes <= 0 ||
		config.MaxBodyBytes > 16<<20 ||
		httpClient == nil {
		return nil, errors.New("GitHub client configuration is invalid")
	}
	baseURL, err := url.Parse(config.BaseURL)
	if err != nil ||
		baseURL.Host == "" ||
		baseURL.RawQuery != "" ||
		baseURL.Fragment != "" ||
		baseURL.User != nil ||
		(baseURL.Scheme != "https" && !isLoopbackHTTP(baseURL)) {
		return nil, errors.New("GitHub API base URL is invalid")
	}
	cloned := *httpClient
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("GitHub API redirect rejected")
	}
	return &Client{
		baseURL:      baseURL,
		repository:   config.Repository,
		token:        config.Token,
		maxPages:     config.MaxPages,
		maxBodyBytes: config.MaxBodyBytes,
		httpClient:   &cloned,
	}, nil
}

// ListIssueComments reads every page up to the configured hard budget.
func (client *Client) ListIssueComments(
	ctx context.Context,
	issueNumber int64,
) ([]IssueComment, error) {
	if client == nil || issueNumber <= 0 {
		return nil, errors.New("Issue comment request is invalid")
	}
	comments := make([]IssueComment, 0)
	for page := 1; page <= client.maxPages; page++ {
		endpoint := client.endpoint(
			"/repos/" + client.repository +
				"/issues/" + strconv.FormatInt(issueNumber, 10) + "/comments",
		)
		query := endpoint.Query()
		query.Set("per_page", "100")
		query.Set("page", strconv.Itoa(page))
		endpoint.RawQuery = query.Encode()

		var payload []struct {
			ID   int64 `json:"id"`
			User struct {
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"user"`
			Body      string   `json:"body"`
			CreatedAt jsonTime `json:"created_at"`
			UpdatedAt jsonTime `json:"updated_at"`
		}
		next, err := client.getJSONPage(ctx, endpoint, &payload)
		if err != nil {
			return nil, err
		}
		if len(payload) > 100 {
			return nil, errors.New("GitHub comment page exceeds item limit")
		}
		for _, comment := range payload {
			if comment.ID <= 0 ||
				comment.User.Login == "" ||
				len(comment.Body) > maxCheckpointComment ||
				comment.CreatedAt.Time.IsZero() ||
				comment.UpdatedAt.Time.IsZero() {
				return nil, errors.New("GitHub comment response is invalid")
			}
			comments = append(comments, IssueComment{
				ID:         comment.ID,
				Author:     comment.User.Login,
				AuthorType: comment.User.Type,
				Body:       comment.Body,
				CreatedAt:  comment.CreatedAt.Time,
				UpdatedAt:  comment.UpdatedAt.Time,
			})
		}
		if next == nil {
			return comments, nil
		}
		if page == client.maxPages {
			return nil, errors.New("GitHub comment pagination exceeds page budget")
		}
		if next.Scheme != client.baseURL.Scheme ||
			next.Host != client.baseURL.Host ||
			next.Path != endpoint.Path ||
			next.Query().Get("per_page") != "100" ||
			next.Query().Get("page") != strconv.Itoa(page+1) ||
			len(next.Query()) != 2 {
			return nil, errors.New("GitHub comment next page is outside request scope")
		}
	}
	return nil, errors.New("GitHub comment pagination did not terminate")
}

func (client *Client) endpoint(path string) url.URL {
	endpoint := *client.baseURL
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") + path
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint
}

func (client *Client) getJSONPage(
	ctx context.Context,
	endpoint url.URL,
	output any,
) (*url.URL, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, errors.New("create GitHub API request")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("Authorization", "Bearer "+client.token)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GitHub API request failed: %w", redactHTTPError(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, fmt.Errorf("GitHub API returned status %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, errors.New("GitHub API returned unexpected content type")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, client.maxBodyBytes+1))
	if err != nil {
		return nil, errors.New("read GitHub API response")
	}
	if int64(len(body)) > client.maxBodyBytes {
		return nil, errors.New("GitHub API response exceeds byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(output); err != nil {
		return nil, fmt.Errorf("decode GitHub API response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("GitHub API response contains trailing JSON")
	}
	return parseNextLink(response.Header.Get("Link"))
}

func parseNextLink(header string) (*url.URL, error) {
	if header == "" {
		return nil, nil
	}
	var next *url.URL
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		sections := strings.Split(part, ";")
		if len(sections) != 2 || strings.TrimSpace(sections[1]) != `rel="next"` {
			continue
		}
		rawURL := strings.TrimSpace(sections[0])
		if len(rawURL) < 2 || rawURL[0] != '<' || rawURL[len(rawURL)-1] != '>' {
			return nil, errors.New("GitHub pagination Link is malformed")
		}
		parsed, err := url.Parse(rawURL[1 : len(rawURL)-1])
		if err != nil || parsed.Host == "" || next != nil {
			return nil, errors.New("GitHub pagination next Link is invalid")
		}
		next = parsed
	}
	return next, nil
}

type jsonTime struct {
	time.Time
}

func (value *jsonTime) UnmarshalJSON(encoded []byte) error {
	if value == nil {
		return errors.New("nil JSON time")
	}
	var raw string
	if err := json.Unmarshal(encoded, &raw); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return err
	}
	value.Time = parsed
	return nil
}
