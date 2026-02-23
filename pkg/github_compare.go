package pkg

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v83/github"
)

// fetchComparisonData collects release notes, commit titles, and changed files between two tags.
func fetchComparisonData(ctx context.Context, gh *github.Client, owner, repo, fromTag, toTag string, maxReleases int, mode string) (comparisonData, error) {
	startCompare := time.Now()
	compare, _, err := gh.Repositories.CompareCommits(ctx, owner, repo, fromTag, toTag, nil)
	compareErr := mapGitHubError(err)
	observeGitHubRequest("compare_commits", compareErr, time.Since(startCompare))
	if compareErr != nil {
		return comparisonData{}, compareErr
	}

	commitTitles := make([]string, 0, len(compare.Commits))
	for _, c := range compare.Commits {
		if c == nil || c.Commit == nil {
			continue
		}
		message := strings.TrimSpace(c.Commit.GetMessage())
		if message == "" {
			continue
		}
		title := strings.SplitN(message, "\n", 2)[0]
		if mode == "deep" {
			sha := c.GetSHA()
			if len(sha) > 7 {
				sha = sha[:7]
			}
			if sha != "" {
				title = sha + ": " + title
			}
		}
		commitTitles = append(commitTitles, title)
	}

	var changedFiles []string
	if mode == "deep" {
		seen := make(map[string]struct{})
		for _, f := range compare.Files {
			if f == nil {
				continue
			}
			name := f.GetFilename()
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			changedFiles = append(changedFiles, name)
		}
	}

	releaseNotes, err := fetchReleaseNotes(ctx, gh, owner, repo, fromTag, toTag, maxReleases)
	if err != nil {
		return comparisonData{}, err
	}

	return comparisonData{
		ReleaseNotes: releaseNotes,
		CommitTitles: commitTitles,
		ChangedFiles: changedFiles,
	}, nil
}

func fetchReleaseNotes(ctx context.Context, gh *github.Client, owner, repo, fromTag, toTag string, maxReleases int) ([]releaseNote, error) {
	notes := []releaseNote{}
	opt := &github.ListOptions{PerPage: 100}
	collecting := false
	endTag := ""

	for {
		startList := time.Now()
		releases, resp, err := gh.Repositories.ListReleases(ctx, owner, repo, opt)
		listErr := mapGitHubError(err)
		observeGitHubRequest("list_releases", listErr, time.Since(startList))
		if listErr != nil {
			return nil, listErr
		}

		for _, rel := range releases {
			if rel == nil {
				continue
			}
			tag := strings.TrimSpace(rel.GetTagName())
			if tag == "" {
				continue
			}

			if !collecting {
				if tag == fromTag || tag == toTag {
					collecting = true
					if tag == fromTag {
						endTag = toTag
					} else {
						endTag = fromTag
					}
				} else {
					continue
				}
			}

			if collecting {
				body := rel.GetBody()
				if len(body) > 5000 {
					body = body[:5000]
				}
				notes = append(notes, releaseNote{Tag: tag, Body: body})

				if tag == endTag {
					return clampReleaseNotes(notes, maxReleases), nil
				}
				if len(notes) >= maxReleases {
					return clampReleaseNotes(notes, maxReleases), nil
				}
			}
		}

		if resp == nil || resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return clampReleaseNotes(notes, maxReleases), nil
}

func clampReleaseNotes(notes []releaseNote, maxReleases int) []releaseNote {
	if maxReleases <= 0 {
		return notes
	}
	if len(notes) > maxReleases {
		return notes[:maxReleases]
	}
	return notes
}

func mapGitHubError(err error) error {
	var ghErr *github.ErrorResponse
	if errors.As(err, &ghErr) && ghErr.Response != nil {
		switch ghErr.Response.StatusCode {
		case http.StatusNotFound:
			return ErrRepoNotFound
		case http.StatusForbidden:
			return ErrRateLimited
		}
	}
	return err
}
