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

		resp, shapeInvalid, err := validateAndNormalizeResponse(modelPayload)
		if err != nil {
			logModelParseFailure(log, "initial", err, model, numPredict, modelPayload)
			if shapeInvalid {
				repairPrompt, promptErr := buildRepairPrompt(modelPayload)
				if promptErr != nil {
					log.Error("failed to build repair prompt", zap.Error(promptErr))
					writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "model returned invalid JSON"})
					return
				}

				repairPayload, repairErr := callOllama(ctx, ollamaBaseURL, repairPrompt, req.Mode)
				if repairErr != nil {
					handleAnalyzeError(w, repairErr, ctx, log)
					return
				}

				resp, _, err = validateAndNormalizeResponse(repairPayload)
				if err != nil {
					logModelParseFailure(log, "repair", err, model, numPredict, repairPayload)
					writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "model returned invalid JSON"})
					return
				}
			} else {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "model returned invalid JSON"})
				return
			}
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
		"You are a release risk analyst.\nOutput MUST be a single JSON object with EXACTLY these top-level keys: risk, summary, breakers, behaviorChanges, upgradeSteps, evidence, meta. Do not add extra keys.\nTypes:\n- risk MUST be an object, not a string: {\"level\":\"low|medium|high\",\"score\":0-100,\"confidence\":\"low|medium|high\",\"reasons\":[...]}\n- summary MUST be an object: {\"highlights\":[...],\"grouped\":[{\"title\":\"...\",\"items\":[...]}]}\n- breakers / behaviorChanges / upgradeSteps / evidence MUST be arrays (can be empty, items are objects).\n- meta MUST be an object.\nNo markdown, no code fences, no commentary. Output must start with { and end with }. No trailing commas. Use double quotes only.\nInput:\n%s",
		string(payload),
	)
	return prompt, nil
}

// validateAndNormalizeResponse validates JSON shape, then normalizes risk level and empty slices.
func validateAndNormalizeResponse(raw []byte) (AnalyzeResponse, bool, error) {
	cleaned := extractJSONObject(raw)
	cleaned = bytes.TrimSpace(cleaned)
	if len(cleaned) == 0 {
		return AnalyzeResponse{}, false, errors.New("empty model response")
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(cleaned, &obj); err != nil {
		return AnalyzeResponse{}, false, err
	}

	if err := validateResponseShape(obj); err != nil {
		return AnalyzeResponse{}, true, err
	}

	var resp AnalyzeResponse
	if err := json.Unmarshal(cleaned, &resp); err != nil {
		return AnalyzeResponse{}, false, err
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

	return resp, false, nil
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

func extractJSONObject(raw []byte) []byte {
	text := string(raw)
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start == -1 || end == -1 || end <= start {
		return raw
	}
	return []byte(text[start : end+1])
}

func validateResponseShape(obj map[string]json.RawMessage) error {
	required := []string{"risk", "summary", "breakers", "behaviorChanges", "upgradeSteps", "evidence", "meta"}
	for _, key := range required {
		if _, ok := obj[key]; !ok {
			return fmt.Errorf("missing key %s", key)
		}
	}

	var risk map[string]json.RawMessage
	if err := json.Unmarshal(obj["risk"], &risk); err != nil {
		return errors.New("risk must be object")
	}
	if reasons, ok := risk["reasons"]; ok && !isJSONArray(reasons) {
		return errors.New("risk.reasons must be array")
	}

	var summary map[string]json.RawMessage
	if err := json.Unmarshal(obj["summary"], &summary); err != nil {
		return errors.New("summary must be object")
	}
	if grouped, ok := summary["grouped"]; ok {
		var groupedItems []json.RawMessage
		if err := json.Unmarshal(grouped, &groupedItems); err != nil {
			return errors.New("summary.grouped must be array")
		}
		for _, item := range groupedItems {
			var groupedObj map[string]json.RawMessage
			if err := json.Unmarshal(item, &groupedObj); err != nil {
				return errors.New("summary.grouped items must be objects")
			}
			if items, ok := groupedObj["items"]; ok && !isJSONArray(items) {
				return errors.New("summary.grouped.items must be array")
			}
		}
	}

	if err := validateArrayOfObjects(obj["breakers"], "breakers", "evidence"); err != nil {
		return err
	}
	if err := validateArrayOfObjects(obj["behaviorChanges"], "behaviorChanges", "evidence"); err != nil {
		return err
	}
	if err := validateArrayOfObjects(obj["upgradeSteps"], "upgradeSteps", "evidence"); err != nil {
		return err
	}
	if err := validateArrayOfObjects(obj["evidence"], "evidence", ""); err != nil {
		return err
	}

	if !isJSONObject(obj["meta"]) {
		return errors.New("meta must be object")
	}
	return nil
}

func isJSONObject(value json.RawMessage) bool {
	return firstNonSpaceByte(value) == '{'
}

func isJSONArray(value json.RawMessage) bool {
	return firstNonSpaceByte(value) == '['
}

func firstNonSpaceByte(value []byte) byte {
	for _, b := range value {
		switch b {
		case ' ', '\n', '\r', '\t':
			continue
		default:
			return b
		}
	}
	return 0
}

func validateArrayOfObjects(raw json.RawMessage, name, nestedArrayKey string) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("%s must be array", name)
	}
	for _, item := range items {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(item, &obj); err != nil {
			return fmt.Errorf("%s items must be objects", name)
		}
		if nestedArrayKey == "" {
			continue
		}
		if nested, ok := obj[nestedArrayKey]; ok && !isJSONArray(nested) {
			return fmt.Errorf("%s.%s must be array", name, nestedArrayKey)
		}
	}
	return nil
}

func buildRepairPrompt(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("empty model response")
	}
	return fmt.Sprintf(
		"Rewrite the following into valid JSON with EXACTLY these top-level keys: risk, summary, breakers, behaviorChanges, upgradeSteps, evidence, meta. Do not add extra keys.\nTypes:\n- risk MUST be an object, not a string: {\"level\":\"low|medium|high\",\"score\":0-100,\"confidence\":\"low|medium|high\",\"reasons\":[...]}\n- summary MUST be an object: {\"highlights\":[...],\"grouped\":[{\"title\":\"...\",\"items\":[...]}]}\n- breakers / behaviorChanges / upgradeSteps / evidence MUST be arrays (can be empty, items are objects).\n- meta MUST be an object.\nNo markdown, no code fences, no commentary. Output must start with { and end with }. No trailing commas. Use double quotes only.\nInvalid JSON:\n%s",
		string(raw),
	), nil
}

func logModelParseFailure(logger *zap.Logger, stage string, err error, model string, numPredict int, payload []byte) {
	if logger == nil {
		return
	}
	head, tail := headTailSnippet(payload, 300)
	logger.Error("invalid model JSON",
		zap.String("stage", stage),
		zap.Error(err),
		zap.String("model", model),
		zap.Int("num_predict", numPredict),
		zap.Int("model_response_len", len(payload)),
		zap.String("model_response_head", head),
		zap.String("model_response_tail", tail),
	)
}

func headTailSnippet(payload []byte, max int) (string, string) {
	if max <= 0 || len(payload) == 0 {
		return "", ""
	}
	if len(payload) <= max {
		text := string(payload)
		return text, text
	}
	head := string(payload[:max])
	tail := string(payload[len(payload)-max:])
	return head, tail
}
