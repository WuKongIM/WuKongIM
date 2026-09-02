package reviewagentgithub

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

type memoryRoundTripper func(*http.Request) (*http.Response, error)

func (transport memoryRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return transport(request)
}

func newMemoryClient(
	t *testing.T,
	maxBodyBytes int64,
	transport memoryRoundTripper,
) *Client {
	t.Helper()
	client, err := NewClient(ClientConfig{
		BaseURL:      "https://api.github.test",
		GraphQLURL:   "https://api.github.test/graphql",
		Repository:   "WuKongIM/WuKongIM",
		Token:        "test-token",
		MaxPages:     3,
		MaxBodyBytes: maxBodyBytes,
	}, &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type": []string{"application/json; charset=utf-8"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

func TestWriteResponseHonorsTheConfiguredByteLimit(t *testing.T) {
	t.Parallel()

	client := newMemoryClient(t, 64, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", request.Method)
		}
		return jsonResponse(
			http.StatusCreated,
			`{"id":1}`+strings.Repeat(" ", 64),
		), nil
	})

	_, err := client.CreateIssueComment(context.Background(), 42, "reviewing")
	if err == nil || err.Error() != "GitHub API write response exceeds byte limit" {
		t.Fatalf("CreateIssueComment() error = %v", err)
	}
}

func TestCanceledGitHubRequestPreservesCancellationIdentity(t *testing.T) {
	t.Parallel()

	client := newMemoryClient(t, 1024, func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.ActorPermission(ctx, "maintainer")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ActorPermission() error = %v, want context.Canceled", err)
	}
}

func TestNewClientKeepsRequestsOnOneBoundedOrigin(t *testing.T) {
	t.Parallel()

	transport := memoryRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unused")
	})
	originalRedirect := func(*http.Request, []*http.Request) error { return nil }
	httpClient := &http.Client{Transport: transport, CheckRedirect: originalRedirect}
	client, err := NewClient(ClientConfig{
		BaseURL:      "https://api.github.test/api/v3",
		GraphQLURL:   "https://api.github.test/graphql",
		Repository:   "WuKongIM/WuKongIM",
		Token:        "token",
		MaxPages:     100,
		MaxBodyBytes: 16 << 20,
	}, httpClient)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client.httpClient == httpClient {
		t.Fatal("NewClient() reused the caller's mutable HTTP client")
	}
	redirectErr := client.httpClient.CheckRedirect(
		&http.Request{}, []*http.Request{{}},
	)
	if redirectErr == nil || redirectErr.Error() != "GitHub API redirect rejected" {
		t.Fatalf("redirect error = %v", redirectErr)
	}
	if err := httpClient.CheckRedirect(&http.Request{}, nil); err != nil {
		t.Fatalf("caller redirect policy was mutated: %v", err)
	}
	endpoint := client.endpoint("/repos/WuKongIM/WuKongIM")
	if got := endpoint.String(); got != "https://api.github.test/api/v3/repos/WuKongIM/WuKongIM" {
		t.Fatalf("endpoint = %q", got)
	}
}

func TestNewClientRejectsAmbiguousOrUnboundedConfiguration(t *testing.T) {
	t.Parallel()

	valid := ClientConfig{
		BaseURL:      "https://api.github.test",
		GraphQLURL:   "https://api.github.test/graphql",
		Repository:   "WuKongIM/WuKongIM",
		Token:        "token",
		MaxPages:     2,
		MaxBodyBytes: 1024,
	}
	tests := []struct {
		name    string
		mutate  func(*ClientConfig)
		nilHTTP bool
	}{
		{name: "repository traversal", mutate: func(config *ClientConfig) { config.Repository = "owner/..repo" }},
		{name: "empty token", mutate: func(config *ClientConfig) { config.Token = "" }},
		{name: "header injection", mutate: func(config *ClientConfig) { config.Token = "token\r\ninjected" }},
		{name: "zero page budget", mutate: func(config *ClientConfig) { config.MaxPages = 0 }},
		{name: "excessive page budget", mutate: func(config *ClientConfig) { config.MaxPages = 101 }},
		{name: "zero response budget", mutate: func(config *ClientConfig) { config.MaxBodyBytes = 0 }},
		{name: "excessive response budget", mutate: func(config *ClientConfig) { config.MaxBodyBytes = (16 << 20) + 1 }},
		{name: "public plaintext API", mutate: func(config *ClientConfig) { config.BaseURL = "http://github.example/api" }},
		{name: "URL credentials", mutate: func(config *ClientConfig) { config.BaseURL = "https://token@api.github.test" }},
		{name: "URL query", mutate: func(config *ClientConfig) { config.BaseURL = "https://api.github.test?redirect=true" }},
		{name: "different GraphQL origin", mutate: func(config *ClientConfig) { config.GraphQLURL = "https://graphql.github.test" }},
		{name: "nil HTTP client", mutate: func(*ClientConfig) {}, nilHTTP: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := valid
			test.mutate(&config)
			var httpClient *http.Client
			if !test.nilHTTP {
				httpClient = &http.Client{}
			}
			if _, err := NewClient(config, httpClient); err == nil {
				t.Fatal("NewClient() unexpectedly accepted configuration")
			}
		})
	}
}

func TestGetJSONPageEnforcesHeadersStatusAndStrictResponseBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		response  func() *http.Response
		transport error
		expected  string
	}{
		{
			name: "transport details are redacted", transport: errors.New("token=secret"),
			expected: "GitHub API request failed: GitHub API request failed",
		},
		{
			name: "status", response: func() *http.Response { return jsonResponse(http.StatusForbidden, `{"message":"secret"}`) },
			expected: "GitHub API returned status 403",
		},
		{
			name: "content type", response: func() *http.Response {
				response := jsonResponse(http.StatusOK, `{}`)
				response.Header.Set("Content-Type", "text/html")
				return response
			},
			expected: "GitHub API returned unexpected content type",
		},
		{
			name: "oversized", response: func() *http.Response { return jsonResponse(http.StatusOK, strings.Repeat("x", 65)) },
			expected: "GitHub API response exceeds byte limit",
		},
		{
			name: "malformed", response: func() *http.Response { return jsonResponse(http.StatusOK, `{`) },
			expected: "decode GitHub API response",
		},
		{
			name: "trailing JSON", response: func() *http.Response { return jsonResponse(http.StatusOK, `{} {}`) },
			expected: "GitHub API response contains trailing JSON",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := newMemoryClient(t, 64, func(request *http.Request) (*http.Response, error) {
				if request.Header.Get("Authorization") != "Bearer test-token" ||
					request.Header.Get("X-GitHub-Api-Version") != githubAPIVersion ||
					request.Header.Get("Accept") != "application/vnd.github+json" {
					t.Fatalf("request headers = %v", request.Header)
				}
				if test.transport != nil {
					return nil, test.transport
				}
				return test.response(), nil
			})
			var output map[string]any
			_, err := client.getJSONPage(
				context.Background(), client.pagedEndpoint("/items", 1), &output,
			)
			if err == nil || err.Error() != test.expected {
				t.Fatalf("getJSONPage() error = %v, want %q", err, test.expected)
			}
		})
	}

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		client := newMemoryClient(t, 1024, func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"value":7}`), nil
		})
		var output struct {
			Value int `json:"value"`
		}
		next, err := client.getJSONPage(
			context.Background(), client.pagedEndpoint("/items", 1), &output,
		)
		if err != nil || next != nil || output.Value != 7 {
			t.Fatalf("getJSONPage() = next %v, output %+v, error %v", next, output, err)
		}
	})
}

func TestNextLinkMustRemainOnTheExactSequentialCollection(t *testing.T) {
	t.Parallel()

	client := newMemoryClient(t, 1024, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unused")
	})
	current := client.pagedEndpoint("/repos/WuKongIM/WuKongIM/pulls/42/files", 1)
	valid := `<https://api.github.test/repos/WuKongIM/WuKongIM/pulls/42/files?page=9&per_page=100>; rel="last", ` +
		`<https://api.github.test/repos/WuKongIM/WuKongIM/pulls/42/files?page=2&per_page=100>; rel="next"`
	next, err := client.parseNextLink(valid, current)
	if err != nil || next == nil || next.Query().Get("page") != "2" {
		t.Fatalf("parseNextLink(valid) = %v, %v", next, err)
	}
	if next, err := client.parseNextLink("", current); err != nil || next != nil {
		t.Fatalf("parseNextLink(empty) = %v, %v", next, err)
	}

	tests := []struct {
		name   string
		header string
	}{
		{name: "malformed target", header: `https://api.github.test/items?page=2&per_page=100; rel="next"`},
		{name: "foreign origin", header: `<https://evil.test/repos/WuKongIM/WuKongIM/pulls/42/files?page=2&per_page=100>; rel="next"`},
		{name: "different path", header: `<https://api.github.test/repos/WuKongIM/WuKongIM/issues?page=2&per_page=100>; rel="next"`},
		{name: "extra query", header: `<https://api.github.test/repos/WuKongIM/WuKongIM/pulls/42/files?page=2&per_page=100&token=x>; rel="next"`},
		{name: "discontinuous page", header: `<https://api.github.test/repos/WuKongIM/WuKongIM/pulls/42/files?page=3&per_page=100>; rel="next"`},
		{name: "missing next relation", header: `<https://api.github.test/items?page=2&per_page=100>; rel="last"`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := client.parseNextLink(test.header, current); err == nil {
				t.Fatal("parseNextLink() unexpectedly accepted Link header")
			}
		})
	}
}

func TestWriteRequestRejectsEncodingAndStrictResponseFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response func() *http.Response
		error    error
		expected string
	}{
		{
			name: "transport error", error: errors.New("https://secret@github.test"),
			expected: "GitHub API request failed",
		},
		{
			name: "unexpected status", response: func() *http.Response { return jsonResponse(http.StatusConflict, `{}`) },
			expected: "GitHub API returned status 409",
		},
		{
			name: "content type", response: func() *http.Response {
				response := jsonResponse(http.StatusCreated, `{}`)
				response.Header.Set("Content-Type", "text/plain")
				return response
			},
			expected: "GitHub API returned unexpected content type",
		},
		{
			name: "malformed", response: func() *http.Response { return jsonResponse(http.StatusCreated, `{`) },
			expected: "decode GitHub API write response",
		},
		{
			name: "trailing", response: func() *http.Response { return jsonResponse(http.StatusCreated, `{} {}`) },
			expected: "GitHub API write response contains trailing JSON",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := newMemoryClient(t, 1024, func(*http.Request) (*http.Response, error) {
				if test.error != nil {
					return nil, test.error
				}
				return test.response(), nil
			})
			var output map[string]any
			err := client.requestJSON(
				context.Background(), http.MethodPost, "/write", map[string]string{"value": "x"},
				&output, http.StatusCreated,
			)
			if err == nil || err.Error() != test.expected || strings.Contains(err.Error(), "secret") {
				t.Fatalf("requestJSON() error = %v, want %q", err, test.expected)
			}
		})
	}

	t.Run("unencodable request", func(t *testing.T) {
		t.Parallel()
		client := newMemoryClient(t, 1024, func(*http.Request) (*http.Response, error) {
			t.Fatal("transport reached for unencodable request")
			return nil, nil
		})
		err := client.requestJSON(
			context.Background(), http.MethodPost, "/write", math.Inf(1), nil, http.StatusNoContent,
		)
		if err == nil || err.Error() != "encode GitHub API request" {
			t.Fatalf("requestJSON() error = %v", err)
		}
	})

	t.Run("no response body required", func(t *testing.T) {
		t.Parallel()
		client := newMemoryClient(t, 1024, func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("Content-Type = %q", request.Header.Get("Content-Type"))
			}
			return jsonResponse(http.StatusNoContent, `ignored`), nil
		})
		if err := client.requestJSON(
			context.Background(), http.MethodDelete, "/write", nil, nil, http.StatusNoContent,
		); err != nil {
			t.Fatalf("requestJSON() error = %v", err)
		}
	})
}

func TestParseGitHubURLAllowsOnlyHTTPSOrLoopbackHTTP(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"https://api.github.com", "http://localhost:8080", "http://127.0.0.1", "http://[::1]:8080",
	} {
		if _, err := parseGitHubURL(value); err != nil {
			t.Fatalf("parseGitHubURL(%q) error = %v", value, err)
		}
	}
	for _, value := range []string{
		"", "http://github.com", "ftp://localhost", "https://user@github.com", "https://github.com?q=x", "https://github.com/#fragment",
	} {
		if _, err := parseGitHubURL(value); err == nil {
			t.Fatalf("parseGitHubURL(%q) unexpectedly succeeded", value)
		}
	}
}

func TestRequestCreationFailureDoesNotReachTransport(t *testing.T) {
	t.Parallel()

	client := newMemoryClient(t, 1024, func(*http.Request) (*http.Response, error) {
		t.Fatal("transport reached for invalid request URL")
		return nil, nil
	})
	endpoint := url.URL{Scheme: "http", Host: "[::1"}
	var output map[string]any
	err := client.requestJSONAt(
		context.Background(), http.MethodPost, endpoint, struct{}{}, &output, http.StatusOK,
	)
	if err == nil || err.Error() != "create GitHub API write request" {
		t.Fatalf("requestJSONAt() error = %v", err)
	}
}

func TestGetJSONPageReturnsValidatedNextLink(t *testing.T) {
	t.Parallel()

	client := newMemoryClient(t, 1024, func(request *http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusOK, `[]`)
		response.Header.Set(
			"Link",
			"<https://api.github.test/items?page=2&per_page=100>; rel=\"next\"",
		)
		if !slices.Equal(request.URL.Query()["page"], []string{"1"}) {
			t.Fatalf("page query = %v", request.URL.Query())
		}
		return response, nil
	})
	var output []any
	next, err := client.getJSONPage(
		context.Background(), client.pagedEndpoint("/items", 1), &output,
	)
	if err != nil || next == nil || next.Query().Get("page") != "2" {
		t.Fatalf("getJSONPage() = next %v, error %v", next, err)
	}
}

func TestWriteResponseReadFailureIsClassified(t *testing.T) {
	t.Parallel()

	client := newMemoryClient(t, 1024, func(*http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusCreated, "")
		response.Body = failingReadCloser{}
		return response, nil
	})
	var output map[string]any
	err := client.requestJSON(
		context.Background(), http.MethodPost, "/write", struct{}{}, &output, http.StatusCreated,
	)
	if err == nil || err.Error() != "read GitHub API write response" {
		t.Fatalf("requestJSON() error = %v", err)
	}
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingReadCloser) Close() error             { return nil }

func TestGetJSONPageReadFailureIsClassified(t *testing.T) {
	t.Parallel()

	client := newMemoryClient(t, 1024, func(*http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusOK, "")
		response.Body = failingReadCloser{}
		return response, nil
	})
	var output map[string]any
	_, err := client.getJSONPage(
		context.Background(), client.pagedEndpoint("/items", 1), &output,
	)
	if err == nil || err.Error() != "read GitHub API response" {
		t.Fatalf("getJSONPage() error = %v", err)
	}
}
