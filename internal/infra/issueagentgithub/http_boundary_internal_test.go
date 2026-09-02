package issueagentgithub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type boundaryRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip boundaryRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTrip(request)
}

type boundaryReadCloser struct {
	err error
}

func (reader *boundaryReadCloser) Read([]byte) (int, error) {
	return 0, reader.err
}

func (*boundaryReadCloser) Close() error { return nil }

func newBoundaryClient(
	t *testing.T,
	maxBodyBytes int64,
	roundTrip boundaryRoundTripper,
) *Client {
	t.Helper()
	client, err := NewClient(ClientConfig{
		BaseURL:      "https://api.example.test/v3",
		Repository:   "WuKongIM/WuKongIM",
		Token:        "installation-secret-token",
		MaxPages:     2,
		MaxBodyBytes: maxBodyBytes,
	}, &http.Client{Transport: roundTrip})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func boundaryResponse(status int, contentType string, body io.ReadCloser) *http.Response {
	response := &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       body,
	}
	if contentType != "" {
		response.Header.Set("Content-Type", contentType)
	}
	return response
}

func boundaryJSONResponse(status int, body string) *http.Response {
	return boundaryResponse(
		status,
		"application/json; charset=utf-8",
		io.NopCloser(strings.NewReader(body)),
	)
}

func TestRequestJSONBuildsScopedAuthenticatedRequest(t *testing.T) {
	t.Parallel()

	client := newBoundaryClient(t, 4096, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPatch {
			t.Fatalf("method = %q", request.Method)
		}
		if request.URL.String() !=
			"https://api.example.test/v3/repos/WuKongIM/WuKongIM/issues/comments/51" {
			t.Fatalf("URL = %q", request.URL.String())
		}
		if got := request.Header.Get("Authorization"); got != "Bearer installation-secret-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := request.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Fatalf("Accept = %q", got)
		}
		if got := request.Header.Get("X-GitHub-Api-Version"); got != githubAPIVersion {
			t.Fatalf("API version = %q", got)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		var input struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if input.Body != "updated status" {
			t.Fatalf("body = %q", input.Body)
		}
		return boundaryJSONResponse(
			http.StatusOK,
			`{"id":51,"body":"updated status"}`,
		), nil
	})

	var output struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	err := client.requestJSON(
		context.Background(),
		http.MethodPatch,
		"/repos/WuKongIM/WuKongIM/issues/comments/51",
		struct {
			Body string `json:"body"`
		}{Body: "updated status"},
		&output,
		http.StatusOK,
	)
	if err != nil {
		t.Fatalf("requestJSON() error = %v", err)
	}
	if output.ID != 51 || output.Body != "updated status" {
		t.Fatalf("output = %#v", output)
	}
}

func TestRequestJSONFailsClosedAndRedactsResponseDetails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		maxBodyBytes    int64
		response        func() (*http.Response, error)
		wantError       string
		wantNotFound    bool
		forbiddenDetail string
	}{
		{
			name: "not found is classified",
			response: func() (*http.Response, error) {
				return boundaryJSONResponse(http.StatusNotFound, `{"message":"missing"}`), nil
			},
			wantError:    "status 404",
			wantNotFound: true,
		},
		{
			name: "rate limit body is discarded",
			response: func() (*http.Response, error) {
				return boundaryJSONResponse(
					http.StatusTooManyRequests,
					`{"message":"installation-secret-token exhausted"}`,
				), nil
			},
			wantError:       "GitHub API returned status 429",
			forbiddenDetail: "installation-secret-token exhausted",
		},
		{
			name: "unexpected content type",
			response: func() (*http.Response, error) {
				return boundaryResponse(
					http.StatusOK,
					"text/plain",
					io.NopCloser(strings.NewReader(`{"ok":true}`)),
				), nil
			},
			wantError: "unexpected content type",
		},
		{
			name:         "oversized response",
			maxBodyBytes: 8,
			response: func() (*http.Response, error) {
				return boundaryJSONResponse(http.StatusOK, `{"value":12345}`), nil
			},
			wantError: "response exceeds byte limit",
		},
		{
			name: "body read failure",
			response: func() (*http.Response, error) {
				return boundaryResponse(
					http.StatusOK,
					"application/json",
					&boundaryReadCloser{err: errors.New("read failed")},
				), nil
			},
			wantError: "read GitHub API response",
		},
		{
			name: "malformed JSON",
			response: func() (*http.Response, error) {
				return boundaryJSONResponse(http.StatusOK, `{"value":`), nil
			},
			wantError: "decode GitHub API response",
		},
		{
			name: "trailing JSON",
			response: func() (*http.Response, error) {
				return boundaryJSONResponse(http.StatusOK, `{} {}`), nil
			},
			wantError: "response contains trailing JSON",
		},
		{
			name: "transport details are redacted",
			response: func() (*http.Response, error) {
				return nil, errors.New("dial installation-secret-token@private.example")
			},
			wantError:       "GitHub API request failed",
			forbiddenDetail: "private.example",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			maxBodyBytes := test.maxBodyBytes
			if maxBodyBytes == 0 {
				maxBodyBytes = 4096
			}
			client := newBoundaryClient(
				t,
				maxBodyBytes,
				func(*http.Request) (*http.Response, error) { return test.response() },
			)
			var output map[string]any
			err := client.requestJSON(
				context.Background(), http.MethodGet,
				"/repos/WuKongIM/WuKongIM/issues/42",
				nil, &output, http.StatusOK,
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
			if test.wantNotFound && !errors.Is(err, ErrNotFound) {
				t.Fatalf("error = %v, want ErrNotFound", err)
			}
			if test.forbiddenDetail != "" && strings.Contains(err.Error(), test.forbiddenDetail) {
				t.Fatalf("error leaked detail %q: %v", test.forbiddenDetail, err)
			}
			if strings.Contains(err.Error(), "installation-secret-token") {
				t.Fatalf("error leaked token: %v", err)
			}
		})
	}
}

func TestRequestJSONRejectsInvalidWritesBeforeTransport(t *testing.T) {
	t.Parallel()

	client := newBoundaryClient(t, 16, func(*http.Request) (*http.Response, error) {
		t.Fatal("transport must not be called")
		return nil, nil
	})

	if err := (*Client)(nil).requestJSON(
		context.Background(), http.MethodPost, "/graphql", nil, nil,
		http.StatusOK,
	); err == nil {
		t.Fatal("nil client was accepted")
	}
	if err := client.requestJSON(
		context.Background(), http.MethodPost, "/graphql", nil, nil,
	); err == nil {
		t.Fatal("empty expected status set was accepted")
	}
	if err := client.requestJSON(
		context.Background(), http.MethodPost, "/graphql", func() {}, nil,
		http.StatusOK,
	); err == nil || !strings.Contains(err.Error(), "encode") {
		t.Fatalf("unsupported input error = %v", err)
	}
	if err := client.requestJSON(
		context.Background(), http.MethodPost, "/graphql",
		map[string]string{"payload": strings.Repeat("x", 32)}, nil,
		http.StatusOK,
	); err == nil || !strings.Contains(err.Error(), "request exceeds byte limit") {
		t.Fatalf("oversized input error = %v", err)
	}
}

func TestRequestJSONHonorsCancellationWithoutLeakingCredentials(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := newBoundaryClient(t, 4096, func(request *http.Request) (*http.Response, error) {
		if !errors.Is(request.Context().Err(), context.Canceled) {
			t.Fatalf("request context error = %v", request.Context().Err())
		}
		return nil, errors.New("installation-secret-token: " + request.Context().Err().Error())
	})
	var output map[string]any
	err := client.requestJSON(
		ctx, http.MethodGet, "/repos/WuKongIM/WuKongIM/issues/42",
		nil, &output, http.StatusOK,
	)
	if err == nil {
		t.Fatal("canceled request succeeded")
	}
	if strings.Contains(err.Error(), "installation-secret-token") {
		t.Fatalf("canceled request leaked token: %v", err)
	}
}

func TestRequestJSONWithoutOutputAcceptsOnlyExpectedStatus(t *testing.T) {
	t.Parallel()

	client := newBoundaryClient(t, 4096, func(*http.Request) (*http.Response, error) {
		return boundaryResponse(
			http.StatusNoContent,
			"",
			io.NopCloser(strings.NewReader("ignored response details")),
		), nil
	})
	if err := client.requestJSON(
		context.Background(), http.MethodDelete,
		"/repos/WuKongIM/WuKongIM/issues/42/labels/ready-for-agent",
		nil, nil, http.StatusNoContent,
	); err != nil {
		t.Fatalf("requestJSON() error = %v", err)
	}
}

func TestPaginationLinkParserRejectsAmbiguousOrUnscopedNextPages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		header    string
		wantURL   string
		wantError bool
	}{
		{name: "absent"},
		{
			name:   "ignores non-next relation",
			header: `<https://api.example.test/items?page=1>; rel="prev"`,
		},
		{
			name:    "one absolute next",
			header:  `<https://api.example.test/items?page=2>; rel="next"`,
			wantURL: "https://api.example.test/items?page=2",
		},
		{
			name:      "malformed brackets",
			header:    `https://api.example.test/items?page=2; rel="next"`,
			wantError: true,
		},
		{
			name:      "relative next",
			header:    `</items?page=2>; rel="next"`,
			wantError: true,
		},
		{
			name: "duplicate next",
			header: `<https://api.example.test/items?page=2>; rel="next", ` +
				`<https://api.example.test/items?page=3>; rel="next"`,
			wantError: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			next, err := parseNextLink(test.header)
			if test.wantError {
				if err == nil {
					t.Fatalf("parseNextLink(%q) succeeded", test.header)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseNextLink(%q) error = %v", test.header, err)
			}
			if test.wantURL == "" {
				if next != nil {
					t.Fatalf("next = %v", next)
				}
				return
			}
			if next == nil || next.String() != test.wantURL {
				t.Fatalf("next = %v, want %q", next, test.wantURL)
			}
		})
	}
}

func TestGetJSONPageRejectsMalformedReadBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		response  func() (*http.Response, error)
		wantError string
	}{
		{
			name: "not found",
			response: func() (*http.Response, error) {
				return boundaryJSONResponse(http.StatusNotFound, `{}`), nil
			},
			wantError: "status 404",
		},
		{
			name: "rate limited",
			response: func() (*http.Response, error) {
				return boundaryJSONResponse(http.StatusTooManyRequests, `{"secret":"hidden"}`), nil
			},
			wantError: "status 429",
		},
		{
			name: "content type",
			response: func() (*http.Response, error) {
				return boundaryResponse(
					http.StatusOK, "text/html",
					io.NopCloser(strings.NewReader("<html>secret</html>")),
				), nil
			},
			wantError: "unexpected content type",
		},
		{
			name: "read failure",
			response: func() (*http.Response, error) {
				return boundaryResponse(
					http.StatusOK, "application/json",
					&boundaryReadCloser{err: errors.New("read failed")},
				), nil
			},
			wantError: "read GitHub API response",
		},
		{
			name: "malformed JSON",
			response: func() (*http.Response, error) {
				return boundaryJSONResponse(http.StatusOK, `[`), nil
			},
			wantError: "decode GitHub API response",
		},
		{
			name: "trailing JSON",
			response: func() (*http.Response, error) {
				return boundaryJSONResponse(http.StatusOK, `[] []`), nil
			},
			wantError: "trailing JSON",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := newBoundaryClient(t, 4096, func(*http.Request) (*http.Response, error) {
				return test.response()
			})
			endpoint, err := url.Parse("https://api.example.test/v3/items")
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}
			var output []map[string]any
			_, err = client.getJSONPage(context.Background(), *endpoint, &output)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("error leaked response details: %v", err)
			}
		})
	}
}
