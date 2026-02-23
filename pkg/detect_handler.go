package pkg

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/go-github/v83/github"
	"go.uber.org/zap"
)

// DetectHandler serves /detect and returns tag information for a given GitHub repo.
func DetectHandler(gh *github.Client, logger *zap.Logger) http.HandlerFunc {
	if logger == nil {
		logger = zap.NewNop()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		log := logger.With(zap.String("handler", "detect"))

		repoURL := r.URL.Query().Get("repo")
		if repoURL == "" {
			log.Warn("missing repo parameter")
			http.Error(w, "missing repo parameter", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		tags, err := GetRepoTags(ctx, gh, repoURL)
		if err != nil {
			if errors.Is(err, ErrRepoNotFound) {
				log.Warn("repository not found", zap.String("repo_url", repoURL))
				http.Error(w, "repository not found", http.StatusNotFound)
				return
			}
			if errors.Is(err, ErrRateLimited) {
				log.Warn("github rate limit exceeded", zap.String("repo_url", repoURL))
				http.Error(w, "github rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			log.Error("failed to fetch tags", zap.String("repo_url", repoURL), zap.Error(err))
			http.Error(w, "could not fetch tags", http.StatusInternalServerError)
			return
		}

		resp := DetectResponse{
			Repo: RepoInfo{
				Url:      repoURL,
				Provider: "github",
			},
			Tags: tags,
		}

		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(resp)
		if err != nil {
			log.Error("failed to encode response", zap.String("repo_url", repoURL), zap.Error(err))
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
		}
	}
}
