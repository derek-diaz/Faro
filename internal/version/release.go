package version

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	releaseAPIURL        = "https://api.github.com/repos/derek-diaz/Faro/releases/latest"
	releasePageURL       = "https://github.com/derek-diaz/Faro/releases"
	releaseCheckInterval = 6 * time.Hour
)

type Release struct {
	Version     string `json:"version"`
	Display     string `json:"display"`
	URL         string `json:"url"`
	Name        string `json:"name,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	PublishedAt string `json:"published_at"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
}

type Checker struct {
	client    *http.Client
	mu        sync.Mutex
	checkedAt time.Time
	latest    *Release
}

func NewChecker() *Checker {
	return &Checker{client: &http.Client{Timeout: 5 * time.Second}}
}

func (checker *Checker) Latest(ctx context.Context) *Release {
	now := time.Now()
	checker.mu.Lock()
	if !checker.checkedAt.IsZero() && now.Sub(checker.checkedAt) < releaseCheckInterval {
		latest := checker.latest
		checker.mu.Unlock()
		return latest
	}
	checker.mu.Unlock()

	latest := fetchLatestRelease(ctx, checker.client)
	checker.mu.Lock()
	checker.checkedAt = now
	checker.latest = latest
	checker.mu.Unlock()
	return latest
}

func fetchLatestRelease(ctx context.Context, client *http.Client) *Release {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPIURL, nil)
	if err != nil {
		return nil
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "Faro/"+Display)
	response, err := client.Do(request)
	if err != nil {
		return nil
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	var payload githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return nil
	}
	if payload.Draft || payload.Prerelease {
		return nil
	}
	releaseVersion, ok := normalizeReleaseVersion(payload.TagName)
	if !ok || !IsNewer(releaseVersion, Number) {
		return nil
	}
	return &Release{
		Version:     releaseVersion,
		Display:     "v" + releaseVersion,
		URL:         releasePageURL + "/tag/" + url.PathEscape(payload.TagName),
		Name:        strings.TrimSpace(payload.Name),
		PublishedAt: payload.PublishedAt,
	}
}

func normalizeReleaseVersion(tag string) (string, bool) {
	version := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return "", false
	}
	for _, part := range parts {
		if part == "" {
			return "", false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return "", false
		}
	}
	return version, true
}

func IsNewer(candidate, current string) bool {
	candidateParts, candidateOK := semanticVersionParts(candidate)
	currentParts, currentOK := semanticVersionParts(current)
	if !candidateOK || !currentOK {
		return false
	}
	for index := range candidateParts {
		if candidateParts[index] != currentParts[index] {
			return candidateParts[index] > currentParts[index]
		}
	}
	return false
}

func semanticVersionParts(value string) ([3]int, bool) {
	var parts [3]int
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	segments := strings.Split(value, ".")
	if len(segments) != len(parts) {
		return parts, false
	}
	for index, segment := range segments {
		parsed, err := strconv.Atoi(segment)
		if err != nil || parsed < 0 {
			return parts, false
		}
		parts[index] = parsed
	}
	return parts, true
}
