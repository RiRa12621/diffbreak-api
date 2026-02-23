package pkg

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

type ollamaRequest struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	Stream  bool   `json:"stream"`
	Options struct {
		Temperature float64 `json:"temperature"`
		NumPredict  int     `json:"num_predict"`
	} `json:"options"`
}

func TestAnalyzeHandlerInvalidBody(t *testing.T) {
	handler := AnalyzeHandler(nil, "http://localhost:11434", zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, "/analyze", strings.NewReader("{"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	assertErrorJSON(t, rec.Body.Bytes())
}

func TestAnalyzeHandlerInvalidRepoURL(t *testing.T) {
	handler := AnalyzeHandler(nil, "http://localhost:11434", zap.NewNop())

	body := `{"repoUrl":"github.com/octo/hello","fromTag":"v1","toTag":"v2","mode":"fast","limits":{"maxReleases":10}}`
	req := httptest.NewRequest(http.MethodPost, "/analyze", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	assertErrorJSON(t, rec.Body.Bytes())
}

func TestAnalyzeHandlerInvalidMode(t *testing.T) {
	handler := AnalyzeHandler(nil, "http://localhost:11434", zap.NewNop())

	body := `{"repoUrl":"https://github.com/octo/hello","fromTag":"v1","toTag":"v2","mode":"slow","limits":{"maxReleases":10}}`
	req := httptest.NewRequest(http.MethodPost, "/analyze", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	assertErrorJSON(t, rec.Body.Bytes())
}

func TestAnalyzeHandlerMissingTags(t *testing.T) {
	handler := AnalyzeHandler(nil, "http://localhost:11434", zap.NewNop())

	body := `{"repoUrl":"https://github.com/octo/hello","fromTag":"","toTag":"v2","mode":"fast","limits":{"maxReleases":10}}`
	req := httptest.NewRequest(http.MethodPost, "/analyze", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	assertErrorJSON(t, rec.Body.Bytes())
}

func TestAnalyzeHandlerHappyPath(t *testing.T) {
	ghMux := http.NewServeMux()
	ghMux.HandleFunc("/repos/octo/hello/compare/v1.0.0...v1.1.0", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"commits": [
				{"sha":"abc1234","commit":{"message":"feat: add API"}},
				{"sha":"def5678","commit":{"message":"fix: edge case"}}
			],
			"files": [
				{"filename":"pkg/api.go"},
				{"filename":"README.md"}
			]
		}`))
	})
	ghMux.HandleFunc("/repos/octo/hello/releases", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"tag_name":"v1.1.0","body":"Release 1.1.0 notes"},
			{"tag_name":"v1.0.0","body":"Release 1.0.0 notes"}
		]`))
	})

	ghClient := newGitHubTestClient(t, ghMux)

	var gotPrompt string
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotPrompt = req.Prompt

		modelResp := `{
			"risk": {"level": "low", "score": 80, "confidence": "medium", "reasons": ["breaking changes"]},
			"summary": {"highlights": ["Major update"], "grouped": [{"title": "Highlights", "items": ["Item"]}]},
			"breakers": [],
			"behaviorChanges": [],
			"upgradeSteps": [],
			"evidence": [],
			"meta": {"repo": {"url": ""}, "fromTag": "", "toTag": "", "generatedAt": ""}
		}`

		resp := map[string]any{
			"response": modelResp,
			"done":     true,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ollama.Close()

	handler := AnalyzeHandler(ghClient, ollama.URL, zap.NewNop())

	body := `{"repoUrl":"https://github.com/octo/hello","fromTag":"v1.0.0","toTag":"v1.1.0","mode":"deep","limits":{"maxReleases":10}}`
	req := httptest.NewRequest(http.MethodPost, "/analyze", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	if !strings.Contains(gotPrompt, "\"changedFiles\"") {
		t.Fatalf("expected prompt to include changedFiles for deep mode")
	}

	var resp AnalyzeResponse
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Risk.Level != "high" {
		t.Fatalf("expected normalized risk level 'high', got %q", resp.Risk.Level)
	}
	if resp.Meta.Repo.Url != "https://github.com/octo/hello" {
		t.Fatalf("unexpected repo url: %s", resp.Meta.Repo.Url)
	}
	if resp.Meta.FromTag != "v1.0.0" || resp.Meta.ToTag != "v1.1.0" {
		t.Fatalf("unexpected tags in meta: %s -> %s", resp.Meta.FromTag, resp.Meta.ToTag)
	}
	if resp.Meta.GeneratedAt == "" {
		t.Fatalf("expected generatedAt to be set")
	}
}

func assertErrorJSON(t *testing.T, payload []byte) {
	t.Helper()
	var resp errorResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		t.Fatalf("expected JSON error, got %v", err)
	}
	if resp.Error == "" {
		t.Fatalf("expected error message")
	}
}
