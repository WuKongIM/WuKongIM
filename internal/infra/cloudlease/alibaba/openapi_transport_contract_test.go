package alibaba

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
)

// openAPITestHTTPClient exercises Alibaba SDK request serialization without opening a loopback listener.
type openAPITestHTTPClient struct {
	handler http.Handler
}

func (c openAPITestHTTPClient) Call(request *http.Request, _ *http.Transport) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	var body []byte
	if request.Body != nil {
		var err error
		body, err = io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
	}
	serverRequest := httptest.NewRequestWithContext(
		request.Context(),
		request.Method,
		request.URL.String(),
		bytes.NewReader(body),
	)
	for key, values := range request.Header {
		for _, value := range values {
			serverRequest.Header.Add(key, value)
		}
	}
	serverRequest.Host = request.Host
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, serverRequest)
	response := recorder.Result()
	response.Request = request
	return response, nil
}
