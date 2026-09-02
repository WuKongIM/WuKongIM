package issueagentgithub_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	"github.com/stretchr/testify/require"
)

type issueMemoryRoundTripper func(*http.Request) (*http.Response, error)

func (transport issueMemoryRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return transport(request)
}

func newIssueMemoryClient(
	t *testing.T,
	handler http.Handler,
) *issueagentgithub.Client {
	t.Helper()
	transport := issueMemoryRoundTripper(func(
		request *http.Request,
	) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Result(), nil
	})
	client, err := issueagentgithub.NewClient(issueagentgithub.ClientConfig{
		BaseURL: "https://api.github.test", Repository: "WuKongIM/WuKongIM",
		Token: "token", MaxPages: 3, MaxBodyBytes: 1 << 20,
	}, &http.Client{Transport: transport})
	require.NoError(t, err)
	return client
}
