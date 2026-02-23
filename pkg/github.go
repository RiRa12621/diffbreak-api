package pkg

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/go-github/v62/github"
)

var (
	ErrRepoNotFound   = errors.New("repo not found")
	ErrRateLimited    = errors.New("github rate limited")
	ErrTooManyTags    = errors.New("too many tags")
	ErrInvalidRepoURL = errors.New("invalid github repo url")
)

func GetRepoTags(ctx context.Context, gh *github.Client, repoURL string) ([]string, error) {
	owner, repo, err := ParseGitHubRepoURL(repoURL)
	if err != nil {
		return nil, err
	}

	var tags []string
	opt := &github.ListOptions{PerPage: 100}

	for {
		startList := time.Now()
		ghTags, resp, err := gh.Repositories.ListTags(ctx, owner, repo, opt)
		mappedErr := mapGitHubError(err)
		observeGitHubRequest("list_tags", mappedErr, time.Since(startList))
		if mappedErr != nil {
			return nil, mappedErr
		}

		for _, t := range ghTags {
			if t != nil && t.Name != nil {
				tags = append(tags, t.GetName())
			}
		}

		if resp == nil || resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return tags, nil
}

func ParseGitHubRepoURL(repoURL string) (owner string, repo string, err error) {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return "", "", ErrInvalidRepoURL
	}

	u, err := url.Parse(repoURL)
	if err != nil {
		return "", "", ErrInvalidRepoURL
	}

	// Must be https
	if u.Scheme != "https" {
		return "", "", ErrInvalidRepoURL
	}

	// Must be github.com exactly
	if !strings.EqualFold(u.Host, "github.com") {
		return "", "", ErrInvalidRepoURL
	}

	path := strings.Trim(u.Path, "/")
	if path == "" {
		return "", "", ErrInvalidRepoURL
	}

	// Remove optional .git suffix
	path = strings.TrimSuffix(path, ".git")

	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return "", "", ErrInvalidRepoURL
	}

	owner = parts[0]
	repo = parts[1]

	if owner == "" || repo == "" {
		return "", "", ErrInvalidRepoURL
	}

	return owner, repo, nil
}
