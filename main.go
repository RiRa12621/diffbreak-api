package main

import (
	"diffbreak/pkg"
	"flag"
	"net/http"

	"go.uber.org/zap"

	"github.com/google/go-github/v62/github"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	logger, _ := zap.NewProduction()
	defer func() {
		_ = logger.Sync()
	}()

	// CLI configuration for Ollama address, HTTP port, and optional GitHub token.
	llmPtr := flag.String("llm", "http://localhost:11434", "Ollama base URL (e.g. http://localhost:11434)")
	portPtr := flag.String("port", "8080", "port to listen on")
	ifacePtr := flag.String("interface", "0.0.0.0", "interface to listen on")
	ghPtr := flag.String("github", "", "GitHub access token to evade rate limits a bit")
	flag.Parse()

	// Create GitHub client (optionally authenticated to reduce rate limiting).
	client := github.NewClient(nil)
	if *ghPtr != "" {
		client = github.NewClient(nil).WithAuthToken(*ghPtr)
	}

	// Use a custom Prometheus registry for app-specific metrics.
	reg := prometheus.NewRegistry()
	pkg.RegisterMetrics(reg)

	// Expose /metrics HTTP endpoint using the created custom registry.
	metricsHandler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})

	http.Handle("/metrics", metricsHandler)
	// Public API endpoints for repo detection and upgrade analysis.
	http.Handle("/detect", pkg.WrapHandler("detect", pkg.DetectHandler(client, logger), logger))
	http.Handle("/api/analyze", pkg.WrapHandler("analyze", pkg.AnalyzeHandler(client, *llmPtr, logger), logger))
	err := http.ListenAndServe(*ifacePtr+":"+*portPtr, nil)
	if err != nil {
		logger.Fatal("starting http server", zap.Error(err))
	}

}
