package pkg

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"go.uber.org/zap"
)

func TestDetectHandlerMissingRepo(t *testing.T) {
	handler := DetectHandler(nil, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/detect", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestDetectHandlerInvalidRepo(t *testing.T) {
	handler := DetectHandler(nil, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/detect?repo=github.com/octo/hello", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestDetectHandlerRepoNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/octo/hello/tags", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	client := newGitHubTestClient(t, mux)
	handler := DetectHandler(client, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/detect?repo=https://github.com/octo/hello", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestDetectHandlerSuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/octo/hello/tags", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"v1.0.0"},{"name":"v1.1.0"}]`))
	})

	client := newGitHubTestClient(t, mux)
	handler := DetectHandler(client, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/detect?repo=https://github.com/octo/hello", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp DetectResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Repo.Url != "https://github.com/octo/hello" {
		t.Fatalf("unexpected repo url: %s", resp.Repo.Url)
	}
	if resp.Repo.Owner != "octo" {
		t.Fatalf("unexpected repo owner: %s", resp.Repo.Owner)
	}
	if resp.Repo.Name != "hello" {
		t.Fatalf("unexpected repo name: %s", resp.Repo.Name)
	}
	if resp.Repo.Provider != "github" {
		t.Fatalf("unexpected provider: %s", resp.Repo.Provider)
	}

	wantTags := []string{"v1.0.0", "v1.1.0"}
	if !reflect.DeepEqual(resp.Tags, wantTags) {
		t.Fatalf("expected tags %v, got %v", wantTags, resp.Tags)
	}
}
