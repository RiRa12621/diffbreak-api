package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestParseGitHubRepoURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		owner   string
		repo    string
		wantErr bool
	}{
		{
			name:  "valid",
			input: "https://github.com/octo/hello",
			owner: "octo",
			repo:  "hello",
		},
		{
			name:  "valid with trailing slash",
			input: "https://github.com/octo/hello/",
			owner: "octo",
			repo:  "hello",
		},
		{
			name:  "valid with .git suffix",
			input: "https://github.com/octo/hello.git",
			owner: "octo",
			repo:  "hello",
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
		{
			name:    "non-https",
			input:   "http://github.com/octo/hello",
			wantErr: true,
		},
		{
			name:    "wrong host",
			input:   "https://gitlab.com/octo/hello",
			wantErr: true,
		},
		{
			name:    "missing repo",
			input:   "https://github.com/octo",
			wantErr: true,
		},
		{
			name:    "too many path parts",
			input:   "https://github.com/octo/hello/extra",
			wantErr: true,
		},
		{
			name:    "no scheme",
			input:   "github.com/octo/hello",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, err := ParseGitHubRepoURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				if err != ErrInvalidRepoURL {
					t.Fatalf("expected ErrInvalidRepoURL, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tt.owner || repo != tt.repo {
				t.Fatalf("expected %s/%s, got %s/%s", tt.owner, tt.repo, owner, repo)
			}
		})
	}
}

func TestGetRepoTagsPagination(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/octo/hello/tags", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")

		switch page {
		case "", "1":
			nextURL := fmt.Sprintf("http://%s%s?page=2", r.Host, r.URL.Path)
			w.Header().Set("Link", fmt.Sprintf("<%s>; rel=\"next\"", nextURL))
			_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "v1.0.0"}, {"name": "v1.1.0"}})
		case "2":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "v2.0.0"}})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})

	client := newGitHubTestClient(t, mux)

	ctx := context.Background()
	tags, err := GetRepoTags(ctx, client, "https://github.com/octo/hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"v1.0.0", "v1.1.0", "v2.0.0"}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("expected %v, got %v", want, tags)
	}
}

func TestGetRepoTagsNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/octo/hello/tags", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	client := newGitHubTestClient(t, mux)

	_, err := GetRepoTags(context.Background(), client, "https://github.com/octo/hello")
	if err != ErrRepoNotFound {
		t.Fatalf("expected ErrRepoNotFound, got %v", err)
	}
}

func TestGetRepoTagsRateLimited(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/octo/hello/tags", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	client := newGitHubTestClient(t, mux)

	_, err := GetRepoTags(context.Background(), client, "https://github.com/octo/hello")
	if err != ErrRateLimited {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
}

func TestGetRepoTagsInvalidURL(t *testing.T) {
	_, err := GetRepoTags(context.Background(), nil, "github.com/octo/hello")
	if err != ErrInvalidRepoURL {
		t.Fatalf("expected ErrInvalidRepoURL, got %v", err)
	}
}

func TestParseGitHubRepoURLRejectsEmptySegments(t *testing.T) {
	_, _, err := ParseGitHubRepoURL("https://github.com//")
	if err != ErrInvalidRepoURL {
		t.Fatalf("expected ErrInvalidRepoURL, got %v", err)
	}
}

func TestParseGitHubRepoURLTrimsWhitespace(t *testing.T) {
	owner, repo, err := ParseGitHubRepoURL("  https://github.com/octo/hello  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "octo" || repo != "hello" {
		t.Fatalf("expected octo/hello, got %s/%s", owner, repo)
	}
}

func TestParseGitHubRepoURLRejectsEnterpriseHost(t *testing.T) {
	_, _, err := ParseGitHubRepoURL("https://github.mycorp.com/octo/hello")
	if err != ErrInvalidRepoURL {
		t.Fatalf("expected ErrInvalidRepoURL, got %v", err)
	}
}

func TestGetRepoTagsReturnsEmptyWhenNoTags(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/octo/empty/tags", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]string{})
	})

	client := newGitHubTestClient(t, mux)

	tags, err := GetRepoTags(context.Background(), client, "https://github.com/octo/empty")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("expected 0 tags, got %v", tags)
	}
}

func TestGetRepoTagsHandlesNilTagEntries(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/octo/hello/tags", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Include an entry with a null name to exercise nil checks.
		w.Write([]byte(`[{"name":"v1.0.0"}, {}]`))
	})

	client := newGitHubTestClient(t, mux)

	tags, err := GetRepoTags(context.Background(), client, "https://github.com/octo/hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"v1.0.0"}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("expected %v, got %v", want, tags)
	}
}

func TestGetRepoTagsBailsOnBadResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/octo/hello/tags", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	client := newGitHubTestClient(t, mux)

	_, err := GetRepoTags(context.Background(), client, "https://github.com/octo/hello")
	if err == nil {
		t.Fatalf("expected error")
	}
}
