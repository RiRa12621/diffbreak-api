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

// AnalyzeHandler handles POST /analyze requests.
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

		ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
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

		model, numPredict := ollamaConfig(req.Mode)
		log.Info("ollama prompt stats",
			zap.Int("prompt_bytes", len(prompt)),
			zap.Int("prompt_tokens_est", len(prompt)/4),
			zap.String("model", model),
			zap.Int("num_predict", numPredict),
		)

		modelPayload, err := callOllama(ctx, ollamaBaseURL, prompt, req.Mode)
		if err != nil {
			handleAnalyzeError(w, err, ctx, log)
			return
		}

		resp, err := validateAndNormalizeResponse(modelPayload)
		if err != nil {
			log.Error("invalid model JSON",
				zap.Error(err),
				zap.String("model", model),
				zap.Int("num_predict", numPredict),
				zap.Int("model_response_len", len(modelPayload)),
				zap.String("model_response_excerpt", truncateForLog(sanitizeForLog(string(modelPayload)), 400)),
			)
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

	prompt := fmt.Sprintf(
		"You are a release risk analyst.\nReturn a single JSON object with keys: risk, summary, breakers, behaviorChanges, upgradeSteps, evidence, meta.\nNo markdown, no code fences, no commentary.\nOutput must start with { and end with }.\nNo trailing commas.\nUse double quotes only.\nInput:\n%s",
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
		text := string(raw)
		start := strings.IndexByte(text, '{')
		end := strings.LastIndexByte(text, '}')
		if start == -1 || end == -1 || end <= start {
			return AnalyzeResponse{}, err
		}
		if err := json.Unmarshal([]byte(text[start:end+1]), &resp); err != nil {
			return AnalyzeResponse{}, err
		}
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

func truncateForLog(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "...(truncated)"
}

func sanitizeForLog(value string) string {
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "\r", "\\r")
	value = strings.ReplaceAll(value, "\t", "\\t")
	return value
}
