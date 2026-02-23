package pkg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v83/github"
	"go.uber.org/zap"
)

// AnalyzeHandler handles POST /api/analyze requests.
func AnalyzeHandler(gh *github.Client, ollamaBaseURL string, logger *zap.Logger) http.HandlerFunc {
	if logger == nil {
		logger = zap.NewNop()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		log := logger.With(zap.String("handler", "analyze"))

		if r.Method != http.MethodPost {
			log.Warn("method not allowed", zap.String("method", r.Method))
			writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		var req AnalyzeRequest
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil {
			log.Warn("invalid JSON body", zap.Error(err))
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})
			return
		}

		req.RepoUrl = strings.TrimSpace(req.RepoUrl)
		if req.RepoUrl == "" {
			log.Warn("missing repoUrl")
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "repoUrl is required"})
			return
		}
		if strings.TrimSpace(req.FromTag) == "" {
			log.Warn("missing fromTag", zap.String("repo_url", req.RepoUrl))
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "fromTag is required"})
			return
		}
		if strings.TrimSpace(req.ToTag) == "" {
			log.Warn("missing toTag", zap.String("repo_url", req.RepoUrl))
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "toTag is required"})
			return
		}
		if req.Mode != "fast" && req.Mode != "deep" {
			log.Warn("invalid mode", zap.String("mode", req.Mode), zap.String("repo_url", req.RepoUrl))
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "mode must be 'fast' or 'deep'"})
			return
		}

		maxReleases := req.Limits.MaxReleases
		if maxReleases == 0 {
			maxReleases = 30
		}
		if maxReleases < 1 {
			maxReleases = 1
		}
		if maxReleases > 60 {
			maxReleases = 60
		}

		owner, repo, err := ParseGitHubRepoURL(req.RepoUrl)
		if err != nil {
			log.Warn("invalid repoUrl", zap.String("repo_url", req.RepoUrl))
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid repoUrl"})
			return
		}

		log = log.With(
			zap.String("repo_url", req.RepoUrl),
			zap.String("from_tag", req.FromTag),
			zap.String("to_tag", req.ToTag),
			zap.String("mode", req.Mode),
		)

		data, err := fetchComparisonData(ctx, gh, owner, repo, req.FromTag, req.ToTag, maxReleases, req.Mode)
		if err != nil {
			handleAnalyzeError(w, err, ctx, log)
			return
		}

		bundle := analysisInputBundle{
			Repo:         req.RepoUrl,
			From:         req.FromTag,
			To:           req.ToTag,
			ReleaseNotes: data.ReleaseNotes,
			CommitTitles: data.CommitTitles,
			ChangedFiles: data.ChangedFiles,
		}

		prompt, err := buildAnalysisPrompt(bundle)
		if err != nil {
			log.Error("failed to build prompt", zap.Error(err))
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to build analysis prompt"})
			return
		}

		modelPayload, err := callOllama(ctx, ollamaBaseURL, prompt)
		if err != nil {
			handleAnalyzeError(w, err, ctx, log)
			return
		}

		resp, err := validateAndNormalizeResponse(modelPayload)
		if err != nil {
			log.Error("invalid model JSON", zap.Error(err))
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "model returned invalid JSON"})
			return
		}

		resp.Meta.Repo.Url = req.RepoUrl
		resp.Meta.FromTag = req.FromTag
		resp.Meta.ToTag = req.ToTag
		resp.Meta.GeneratedAt = time.Now().UTC().Format(time.RFC3339)

		log.Info("analysis completed", zap.Int("risk_score", resp.Risk.Score), zap.String("risk_level", resp.Risk.Level))
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleAnalyzeError(w http.ResponseWriter, err error, ctx context.Context, logger *zap.Logger) {
	if logger == nil {
		logger = zap.NewNop()
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		logger.Warn("request timed out")
		writeJSON(w, http.StatusGatewayTimeout, errorResponse{Error: "request timed out"})
		return
	}
	if errors.Is(err, ErrRepoNotFound) {
		logger.Warn("repository not found")
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "repository not found"})
		return
	}
	if errors.Is(err, ErrRateLimited) {
		logger.Warn("github rate limit exceeded")
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "github rate limit exceeded"})
		return
	}
	logger.Error("internal server error", zap.Error(err))
	writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// buildAnalysisPrompt builds a compact prompt instructing the model to return strict JSON.
func buildAnalysisPrompt(bundle analysisInputBundle) (string, error) {
	payload, err := json.Marshal(bundle)
	if err != nil {
		return "", err
	}

	schema := `{
  "risk": { "level": "low|medium|high", "score": 0-100, "confidence": "low|medium|high", "reasons": ["..."] },
  "summary": { "highlights": ["..."], "grouped": [ { "title": "...", "items": ["..."] } ] },
  "breakers": [ { "title": "...", "severity": "low|medium|high", "reason": "...", "evidence": [ { "label": "...", "url": "..." } ] } ],
  "behaviorChanges": [ { "title": "...", "reason": "...", "evidence": [ { "label": "...", "url": "..." } ] } ],
  "upgradeSteps": [ { "step": "...", "why": "...", "evidence": [ { "label": "...", "url": "..." } ] } ],
  "evidence": [ { "label": "...", "url": "...", "kind": "release|pr|compare|commit" } ],
  "meta": { "repo": { "url": "..." }, "fromTag": "...", "toTag": "...", "generatedAt": "RFC3339 timestamp" }
}`

	prompt := fmt.Sprintf(
		"You are a release risk analyst. Return ONLY valid JSON matching this schema exactly, with no extra keys and no markdown.\nSchema:\n%s\nInput:\n%s",
		schema,
		string(payload),
	)
	return prompt, nil
}

// validateAndNormalizeResponse validates JSON and normalizes risk level and empty slices.
func validateAndNormalizeResponse(raw []byte) (AnalyzeResponse, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return AnalyzeResponse{}, errors.New("empty model response")
	}

	var resp AnalyzeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return AnalyzeResponse{}, err
	}

	resp.Risk.Score = clampScore(resp.Risk.Score)
	expectedLevel := riskLevelForScore(resp.Risk.Score)
	if resp.Risk.Level != expectedLevel {
		resp.Risk.Level = expectedLevel
	}
	if resp.Risk.Reasons == nil {
		resp.Risk.Reasons = []string{}
	}
	if resp.Summary.Highlights == nil {
		resp.Summary.Highlights = []string{}
	}
	if resp.Summary.Grouped == nil {
		resp.Summary.Grouped = []GroupedSummary{}
	}
	for i := range resp.Summary.Grouped {
		if resp.Summary.Grouped[i].Items == nil {
			resp.Summary.Grouped[i].Items = []string{}
		}
	}
	if resp.Breakers == nil {
		resp.Breakers = []Breaker{}
	}
	for i := range resp.Breakers {
		if resp.Breakers[i].Evidence == nil {
			resp.Breakers[i].Evidence = []EvidenceLink{}
		}
	}
	if resp.BehaviorChanges == nil {
		resp.BehaviorChanges = []BehaviorChange{}
	}
	for i := range resp.BehaviorChanges {
		if resp.BehaviorChanges[i].Evidence == nil {
			resp.BehaviorChanges[i].Evidence = []EvidenceLink{}
		}
	}
	if resp.UpgradeSteps == nil {
		resp.UpgradeSteps = []UpgradeStep{}
	}
	for i := range resp.UpgradeSteps {
		if resp.UpgradeSteps[i].Evidence == nil {
			resp.UpgradeSteps[i].Evidence = []EvidenceLink{}
		}
	}
	if resp.Evidence == nil {
		resp.Evidence = []EvidenceItem{}
	}

	return resp, nil
}

func clampScore(score int) int {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func riskLevelForScore(score int) string {
	switch {
	case score <= 24:
		return "low"
	case score <= 59:
		return "medium"
	default:
		return "high"
	}
}
