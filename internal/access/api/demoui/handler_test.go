package demoui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedDemoBundleHasNoRemoteRuntimeAssets(t *testing.T) {
	forbidden := []string{
		"api.dicebear.com",
		"img1.baidu.com",
		"hm.baidu.com",
		"cdnjs.cloudflare.com",
	}
	err := fs.WalkDir(embeddedDist, "dist", func(assetPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		content, err := fs.ReadFile(embeddedDist, assetPath)
		if err != nil {
			return err
		}
		for _, remote := range forbidden {
			if strings.Contains(string(content), remote) {
				t.Errorf("embedded asset %s contains remote runtime dependency %q", assetPath, remote)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded Demo bundle: %v", err)
	}
}

func TestHandlerRevalidatesDemoIndex(t *testing.T) {
	handler := Handler()
	initial := httptest.NewRecorder()
	handler.ServeHTTP(initial, httptest.NewRequest(http.MethodGet, "/", nil))
	if initial.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", initial.Code, http.StatusOK, initial.Body.String())
	}
	if contentType := initial.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", contentType)
	}
	if cacheControl := initial.Header().Get("Cache-Control"); cacheControl != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cacheControl)
	}
	etag := initial.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag is empty")
	}

	revalidated := httptest.NewRecorder()
	revalidatedRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	revalidatedRequest.Header.Set("If-None-Match", etag)
	handler.ServeHTTP(revalidated, revalidatedRequest)
	if revalidated.Code != http.StatusNotModified {
		t.Fatalf("revalidation status = %d, want %d", revalidated.Code, http.StatusNotModified)
	}
	if revalidated.Body.Len() != 0 {
		t.Fatalf("revalidation body length = %d, want 0", revalidated.Body.Len())
	}
}

func TestHandlerServesEveryDemoIndexAssetWithProductionCaching(t *testing.T) {
	handler := Handler()
	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))

	references := regexp.MustCompile(`(?:src|href)="(/demo/(?:assets/[^\"]+|logo\.png))"`).FindAllStringSubmatch(index.Body.String(), -1)
	if len(references) == 0 {
		t.Fatalf("embedded index contains no Demo asset references: %s", index.Body.String())
	}
	for _, reference := range references {
		publicPath := reference[1]
		handlerPath := strings.TrimPrefix(publicPath, "/demo")
		t.Run(publicPath, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, handlerPath, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
			}
			wantCache := "no-cache"
			if strings.HasPrefix(publicPath, "/demo/assets/") {
				wantCache = "public, max-age=31536000, immutable"
			}
			if cacheControl := recorder.Header().Get("Cache-Control"); cacheControl != wantCache {
				t.Fatalf("Cache-Control = %q, want %q", cacheControl, wantCache)
			}
		})
	}
}

func TestHandlerDoesNotMaskMissingDemoAssets(t *testing.T) {
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
}

func TestHandlerSupportsHeadAndRejectsMutations(t *testing.T) {
	head := httptest.NewRecorder()
	Handler().ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/", nil))
	if head.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want %d", head.Code, http.StatusOK)
	}
	if head.Body.Len() != 0 {
		t.Fatalf("HEAD body length = %d, want 0", head.Body.Len())
	}
	if head.Header().Get("Content-Length") == "" {
		t.Fatal("HEAD Content-Length is empty")
	}

	mutation := httptest.NewRecorder()
	Handler().ServeHTTP(mutation, httptest.NewRequest(http.MethodPost, "/", nil))
	if mutation.Code != http.StatusNotFound {
		t.Fatalf("POST status = %d, want %d", mutation.Code, http.StatusNotFound)
	}
}
