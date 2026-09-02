package reviewagentgithub_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	github "github.com/WuKongIM/WuKongIM/internal/infra/reviewagentgithub"
	"github.com/stretchr/testify/require"
)

const reviewMemoryBaseURL = "https://api.github.test"

type reviewMemoryRoundTripper func(*http.Request) (*http.Response, error)

func (transport reviewMemoryRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return transport(request)
}

func newReviewMemoryClient(
	t *testing.T,
	maxPages int,
	maxBodyBytes int64,
	handler http.Handler,
) *github.Client {
	t.Helper()
	transport := reviewMemoryRoundTripper(func(
		request *http.Request,
	) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Result(), nil
	})
	client, err := github.NewClient(github.ClientConfig{
		BaseURL: reviewMemoryBaseURL, GraphQLURL: reviewMemoryBaseURL + "/graphql",
		Repository: "WuKongIM/WuKongIM", Token: "token",
		MaxPages: maxPages, MaxBodyBytes: maxBodyBytes,
	}, &http.Client{Transport: transport})
	require.NoError(t, err)
	return client
}
