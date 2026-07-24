package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSiteHandler(t *testing.T) {
	site := fstest.MapFS{
		"index.html": {
			Data: []byte("overview"),
		},
		"quickstart/index.html": {
			Data: []byte("quickstart"),
		},
	}
	handler := newSiteHandler(site)

	t.Run("a request to the root redirects to the site base path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusTemporaryRedirect {
			t.Fatalf("expected status %d, got %d", http.StatusTemporaryRedirect, rec.Code)
		}
		if location := rec.Header().Get("Location"); location != siteBasePath {
			t.Fatalf("expected redirect to %q, got %q", siteBasePath, location)
		}
	})

	t.Run("a request under the site base path serves generated content", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, siteBasePath+"quickstart/", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
		}
		if body := strings.TrimSpace(rec.Body.String()); body != "quickstart" {
			t.Fatalf("expected quickstart content, got %q", body)
		}
	})

	t.Run("an unprefixed page request returns not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/quickstart/", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
		}
	})
}
