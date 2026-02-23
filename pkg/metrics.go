package pkg

import (
	"errors"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// HttpRequestCounter tracks total HTTP requests by handler, method, and status code.
var HttpRequestCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "http_requests_total",
	Help: "Total number of HTTP requests received",
}, []string{"handler", "method", "status"})

// HttpRequestDuration tracks request latency by handler, method, and status code.
var HttpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "http_request_duration_seconds",
	Help:    "HTTP request latency in seconds",
	Buckets: prometheus.DefBuckets,
}, []string{"handler", "method", "status"})

// GitHubRequestCounter tracks GitHub API calls by operation and status.
var GitHubRequestCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "github_requests_total",
	Help: "Total number of GitHub API requests",
}, []string{"operation", "status"})

// GitHubRequestDuration tracks GitHub API latency by operation and status.
var GitHubRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "github_request_duration_seconds",
	Help:    "GitHub API request latency in seconds",
	Buckets: prometheus.DefBuckets,
}, []string{"operation", "status"})

// OllamaRequestCounter tracks Ollama API calls by status.
var OllamaRequestCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "ollama_requests_total",
	Help: "Total number of Ollama API requests",
}, []string{"status"})

// OllamaRequestDuration tracks Ollama API latency by status.
var OllamaRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "ollama_request_duration_seconds",
	Help:    "Ollama API request latency in seconds",
	Buckets: prometheus.DefBuckets,
}, []string{"status"})

// RegisterMetrics registers all application metrics with the provided registry.
func RegisterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(
		HttpRequestCounter,
		HttpRequestDuration,
		GitHubRequestCounter,
		GitHubRequestDuration,
		OllamaRequestCounter,
		OllamaRequestDuration,
	)
}

func observeHTTPRequest(handler, method string, status int, duration time.Duration) {
	statusLabel := strconv.Itoa(status)
	HttpRequestCounter.WithLabelValues(handler, method, statusLabel).Inc()
	HttpRequestDuration.WithLabelValues(handler, method, statusLabel).Observe(duration.Seconds())
}

func observeGitHubRequest(operation string, err error, duration time.Duration) {
	status := "ok"
	if err != nil {
		switch {
		case errors.Is(err, ErrRepoNotFound):
			status = "not_found"
		case errors.Is(err, ErrRateLimited):
			status = "rate_limited"
		default:
			status = "error"
		}
	}
	GitHubRequestCounter.WithLabelValues(operation, status).Inc()
	GitHubRequestDuration.WithLabelValues(operation, status).Observe(duration.Seconds())
}

func observeOllamaRequest(status string, duration time.Duration) {
	OllamaRequestCounter.WithLabelValues(status).Inc()
	OllamaRequestDuration.WithLabelValues(status).Observe(duration.Seconds())
}
