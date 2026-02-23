package pkg

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v83/github"
)

func newGitHubTestClient(t *testing.T, handler http.Handler) *github.Client {
	t.Helper()

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	client := github.NewClient(ts.Client())
	baseURL, err := url.Parse(ts.URL + "/")
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	client.BaseURL = baseURL
	client.UploadURL = baseURL

	return client
}
