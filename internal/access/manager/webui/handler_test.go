package webui

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestHandlerServesEmbeddedIndexForSPARoutes(t *testing.T) {
	handler := Handler()
	for _, requestPath := range []string{"/", "/cluster/nodes"} {
		t.Run(requestPath, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, requestPath, nil)

			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
			}
			if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
				t.Fatalf("Content-Type = %q, want text/html", contentType)
			}
			if cacheControl := recorder.Header().Get("Cache-Control"); cacheControl != "no-cache" {
				t.Fatalf("Cache-Control = %q, want no-cache", cacheControl)
			}
			if !strings.Contains(recorder.Body.String(), `<div id="root"></div>`) {
				t.Fatalf("body does not contain the manager app root: %s", recorder.Body.String())
			}
		})
	}
}

func TestHandlerDoesNotMaskMissingStaticAssetsWithSPAIndex(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)

	Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("missing asset returned the SPA index: %s", recorder.Body.String())
	}
}

func TestHandlerRejectsManagerAPIAndNonReadRequests(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "manager root", method: http.MethodGet, path: "/manager"},
		{name: "manager route", method: http.MethodGet, path: "/manager/not-found"},
		{name: "SPA mutation", method: http.MethodPost, path: "/cluster/nodes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tt.method, tt.path, nil)

			Handler().ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
			}
		})
	}
}

func TestHandlerSupportsIndexRevalidationAndHead(t *testing.T) {
	handler := Handler()
	initial := httptest.NewRecorder()
	handler.ServeHTTP(initial, httptest.NewRequest(http.MethodGet, "/", nil))
	etag := initial.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag is empty")
	}

	revalidated := httptest.NewRecorder()
	revalidatedRequest := httptest.NewRequest(http.MethodGet, "/cluster/nodes", nil)
	revalidatedRequest.Header.Set("If-None-Match", etag)
	handler.ServeHTTP(revalidated, revalidatedRequest)
	if revalidated.Code != http.StatusNotModified {
		t.Fatalf("revalidation status = %d, want %d", revalidated.Code, http.StatusNotModified)
	}
	if revalidated.Body.Len() != 0 {
		t.Fatalf("revalidation body length = %d, want 0", revalidated.Body.Len())
	}

	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/", nil))
	if head.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want %d", head.Code, http.StatusOK)
	}
	if head.Body.Len() != 0 {
		t.Fatalf("HEAD body length = %d, want 0", head.Body.Len())
	}
	if head.Header().Get("Content-Length") == "" {
		t.Fatal("HEAD Content-Length is empty")
	}
}

func TestHandlerServesEmbeddedPublicAsset(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/favicon.svg", nil)

	Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "image/svg+xml") {
		t.Fatalf("Content-Type = %q, want image/svg+xml", contentType)
	}
	if cacheControl := recorder.Header().Get("Cache-Control"); cacheControl != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cacheControl)
	}
	if !strings.Contains(recorder.Body.String(), "<svg") {
		t.Fatalf("body does not contain SVG content: %s", recorder.Body.String())
	}
}

func TestHandlerServesEveryAssetReferencedByEmbeddedIndex(t *testing.T) {
	handler := Handler()
	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))

	references := regexp.MustCompile(`(?:src|href)="(/(?:assets/[^\"]+|favicon\.svg|icons\.svg))"`).FindAllStringSubmatch(index.Body.String(), -1)
	if len(references) == 0 {
		t.Fatalf("embedded index contains no asset references: %s", index.Body.String())
	}
	for _, reference := range references {
		assetPath := reference[1]
		t.Run(assetPath, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, assetPath, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
			}
			if strings.HasPrefix(assetPath, "/assets/") {
				if cacheControl := recorder.Header().Get("Cache-Control"); cacheControl != "public, max-age=31536000, immutable" {
					t.Fatalf("Cache-Control = %q, want immutable", cacheControl)
				}
			}
		})
	}
}
