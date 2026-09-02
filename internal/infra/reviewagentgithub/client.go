package reviewagentgithub

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
	"strconv"
	"strings"
)

// ClientConfig bounds one repository-scoped GitHub client.
type ClientConfig struct {
	BaseURL      string
	GraphQLURL   string
	Repository   string
	Token        string
	MaxPages     int
	MaxBodyBytes int64
}

// Client performs bounded GitHub reads and the narrow writes implemented by
// dedicated adapters.
type Client struct {
	baseURL      *url.URL
	graphqlURL   *url.URL
	repository   string
	token        string
	maxPages     int
	maxBodyBytes int64
	httpClient   *http.Client
}

// NewClient constructs a redirect-rejecting, repository-specific client.
func NewClient(config ClientConfig, httpClient *http.Client) (*Client, error) {
	if !repositoryPattern.MatchString(config.Repository) ||
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
	baseURL, err := parseGitHubURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	graphqlURL, err := parseGitHubURL(config.GraphQLURL)
	if err != nil {
		return nil, err
	}
	if baseURL.Scheme != graphqlURL.Scheme ||
		baseURL.Host != graphqlURL.Host {
		return nil, errors.New("GitHub API endpoints have different origins")
	}
	cloned := *httpClient
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("GitHub API redirect rejected")
	}
	return &Client{
		baseURL: baseURL, graphqlURL: graphqlURL,
		repository: config.Repository, token: config.Token,
		maxPages: config.MaxPages, maxBodyBytes: config.MaxBodyBytes,
		httpClient: &cloned,
	}, nil
}

func parseGitHubURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil ||
		parsed.Host == "" ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		parsed.User != nil ||
		(parsed.Scheme != "https" && !isLoopbackHTTP(parsed)) {
		return nil, errors.New("GitHub API URL is invalid")
	}
	return parsed, nil
}

func (client *Client) endpoint(pathValue string) url.URL {
	endpoint := *client.baseURL
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") + pathValue
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint
}

func (client *Client) getJSON(
	ctx context.Context,
	pathValue string,
	output any,
) error {
	endpoint := client.endpoint(pathValue)
	_, err := client.getJSONPage(ctx, endpoint, output)
	return err
}

func (client *Client) getJSONPage(
	ctx context.Context,
	endpoint url.URL,
	output any,
) (*url.URL, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint.String(),
		nil,
	)
	if err != nil {
		return nil, errors.New("create GitHub API request")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("Authorization", "Bearer "+client.token)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf(
			"GitHub API request failed: %w",
			redactHTTPError(err),
		)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf(
			"GitHub API returned status %d",
			response.StatusCode,
		)
	}
	mediaType, _, err := mime.ParseMediaType(
		response.Header.Get("Content-Type"),
	)
	if err != nil || mediaType != "application/json" {
		return nil, errors.New(
			"GitHub API returned unexpected content type",
		)
	}
	body, err := io.ReadAll(io.LimitReader(
		response.Body,
		client.maxBodyBytes+1,
	))
	if err != nil {
		return nil, errors.New("read GitHub API response")
	}
	if int64(len(body)) > client.maxBodyBytes {
		return nil, errors.New("GitHub API response exceeds byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(output); err != nil {
		return nil, errors.New("decode GitHub API response")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("GitHub API response contains trailing JSON")
	}
	return client.parseNextLink(response.Header.Get("Link"), endpoint)
}

func (client *Client) pagedEndpoint(
	pathValue string,
	page int,
) url.URL {
	endpoint := client.endpoint(pathValue)
	query := endpoint.Query()
	query.Set("per_page", "100")
	query.Set("page", strconv.Itoa(page))
	endpoint.RawQuery = query.Encode()
	return endpoint
}

func (client *Client) parseNextLink(
	header string,
	current url.URL,
) (*url.URL, error) {
	if strings.TrimSpace(header) == "" {
		return nil, nil
	}
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(item), ";")
		if len(parts) != 2 || strings.TrimSpace(parts[1]) != `rel="next"` {
			continue
		}
		target := strings.TrimSpace(parts[0])
		if len(target) < 3 || target[0] != '<' ||
			target[len(target)-1] != '>' {
			return nil, errors.New("GitHub pagination Link is invalid")
		}
		parsed, err := url.Parse(target[1 : len(target)-1])
		if err != nil ||
			parsed.Scheme != client.baseURL.Scheme ||
			parsed.Host != client.baseURL.Host ||
			parsed.Path != current.Path ||
			parsed.Query().Get("per_page") != "100" ||
			len(parsed.Query()) != 2 {
			return nil, errors.New("GitHub next page is outside request scope")
		}
		currentPage, err := strconv.Atoi(current.Query().Get("page"))
		if err != nil ||
			parsed.Query().Get("page") != strconv.Itoa(currentPage+1) {
			return nil, errors.New("GitHub next page is discontinuous")
		}
		return parsed, nil
	}
	return nil, errors.New("GitHub pagination Link lacks next relation")
}

func (client *Client) requestJSON(
	ctx context.Context,
	method string,
	pathValue string,
	input any,
	output any,
	expectedStatus int,
) error {
	endpoint := client.endpoint(pathValue)
	return client.requestJSONAt(
		ctx,
		method,
		endpoint,
		input,
		output,
		expectedStatus,
	)
}

func (client *Client) requestGraphQL(
	ctx context.Context,
	input any,
	output any,
) error {
	return client.requestJSONAt(
		ctx,
		http.MethodPost,
		*client.graphqlURL,
		input,
		output,
		http.StatusOK,
	)
}

func (client *Client) requestJSONAt(
	ctx context.Context,
	method string,
	endpoint url.URL,
	input any,
	output any,
	expectedStatus int,
) error {
	encoded, err := json.Marshal(input)
	if err != nil || int64(len(encoded)) > client.maxBodyBytes {
		return errors.New("encode GitHub API request")
	}
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		endpoint.String(),
		bytes.NewReader(encoded),
	)
	if err != nil {
		return errors.New("create GitHub API write request")
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("Authorization", "Bearer "+client.token)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return redactHTTPError(err)
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf(
			"GitHub API returned status %d",
			response.StatusCode,
		)
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(
		response.Header.Get("Content-Type"),
	)
	if err != nil || mediaType != "application/json" {
		return errors.New("GitHub API returned unexpected content type")
	}
	body, err := io.ReadAll(io.LimitReader(
		response.Body,
		client.maxBodyBytes+1,
	))
	if err != nil {
		return errors.New("read GitHub API write response")
	}
	if int64(len(body)) > client.maxBodyBytes {
		return errors.New("GitHub API write response exceeds byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(output); err != nil {
		return errors.New("decode GitHub API write response")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("GitHub API write response contains trailing JSON")
	}
	return nil
}
