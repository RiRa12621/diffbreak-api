package pkg

import "net/http"

// RepoInfo describes the repository a request or response refers to.
type RepoInfo struct {
	Url      string `json:"url"`
	Owner    string `json:"owner"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// DetectResponse is the response returned by the /detect endpoint.
type DetectResponse struct {
	Repo        RepoInfo `json:"repo"`
	Tags        []string `json:"tags"`
	DefaultFrom string   `json:"defaultFrom,omitempty"`
	DefaultTo   string   `json:"defaultTo,omitempty"`
}

// AnalyzeRequest is the input payload expected by the /api/analyze endpoint.
type AnalyzeRequest struct {
	RepoUrl string `json:"repoUrl"`
	FromTag string `json:"fromTag"`
	ToTag   string `json:"toTag"`
	Mode    string `json:"mode"`
	Limits  struct {
		MaxReleases int `json:"maxReleases"`
	} `json:"limits"`
}

// AnalyzeResponse is the structured analysis result returned by /api/analyze.
type AnalyzeResponse struct {
	Risk            RiskInfo         `json:"risk"`
	Summary         SummaryInfo      `json:"summary"`
	Breakers        []Breaker        `json:"breakers"`
	BehaviorChanges []BehaviorChange `json:"behaviorChanges"`
	UpgradeSteps    []UpgradeStep    `json:"upgradeSteps"`
	Evidence        []EvidenceItem   `json:"evidence"`
	Meta            MetaInfo         `json:"meta"`
}

// RiskInfo captures the overall migration risk.
type RiskInfo struct {
	Level      string   `json:"level"`
	Score      int      `json:"score"`
	Confidence string   `json:"confidence"`
	Reasons    []string `json:"reasons"`
}

// SummaryInfo groups key highlights and themed summaries.
type SummaryInfo struct {
	Highlights []string         `json:"highlights"`
	Grouped    []GroupedSummary `json:"grouped"`
}

// GroupedSummary represents a titled list of summary items.
type GroupedSummary struct {
	Title string   `json:"title"`
	Items []string `json:"items"`
}

// Breaker describes a breaking change and its evidence.
type Breaker struct {
	Title    string         `json:"title"`
	Severity string         `json:"severity"`
	Reason   string         `json:"reason"`
	Evidence []EvidenceLink `json:"evidence"`
}

// BehaviorChange describes a non-breaking behavioral change.
type BehaviorChange struct {
	Title    string         `json:"title"`
	Reason   string         `json:"reason"`
	Evidence []EvidenceLink `json:"evidence"`
}

// UpgradeStep is a concrete migration step with rationale.
type UpgradeStep struct {
	Step     string         `json:"step"`
	Why      string         `json:"why"`
	Evidence []EvidenceLink `json:"evidence"`
}

// EvidenceItem is a top-level evidence entry.
type EvidenceItem struct {
	Label string `json:"label"`
	Url   string `json:"url"`
	Kind  string `json:"kind"`
}

// EvidenceLink is an evidence reference nested under a section.
type EvidenceLink struct {
	Label string `json:"label"`
	Url   string `json:"url"`
}

// MetaInfo captures request and generation metadata.
type MetaInfo struct {
	Repo        RepoMeta `json:"repo"`
	FromTag     string   `json:"fromTag"`
	ToTag       string   `json:"toTag"`
	GeneratedAt string   `json:"generatedAt"`
}

// RepoMeta identifies the repository analyzed.
type RepoMeta struct {
	Url string `json:"url"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type analysisInputBundle struct {
	Repo         string        `json:"repo"`
	From         string        `json:"from"`
	To           string        `json:"to"`
	ReleaseNotes []releaseNote `json:"releaseNotes"`
	CommitTitles []string      `json:"commitTitles"`
	ChangedFiles []string      `json:"changedFiles,omitempty"`
}

type releaseNote struct {
	Tag  string `json:"tag"`
	Body string `json:"body"`
}

type comparisonData struct {
	ReleaseNotes []releaseNote
	CommitTitles []string
	ChangedFiles []string
}

type ollamaGenerateRequest struct {
	Model   string        `json:"model"`
	Prompt  string        `json:"prompt"`
	Stream  bool          `json:"stream"`
	Options ollamaOptions `json:"options"`
}

type ollamaOptions struct {
	Temperature float64 `json:"temperature"`
	NumPredict  int     `json:"num_predict"`
}

type ollamaGenerateResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
	Error    string `json:"error"`
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}
