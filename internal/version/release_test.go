package version

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		candidate string
		current   string
		want      bool
	}{
		{candidate: "v0.10.0", current: "0.9.2", want: true},
		{candidate: "1.0.0", current: "v0.9.1", want: true},
		{candidate: "0.9.0", current: "0.9.1", want: false},
		{candidate: "0.8.9", current: "0.9.1", want: false},
		{candidate: "latest", current: "0.9.1", want: false},
	}
	for _, test := range tests {
		if got := IsNewer(test.candidate, test.current); got != test.want {
			t.Errorf("IsNewer(%q, %q) = %t, want %t", test.candidate, test.current, got, test.want)
		}
	}
}

func TestFetchLatestRelease(t *testing.T) {
	const mockCurrentVersion = "2.4.6"
	client := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"tag_name":"v2.4.7","name":"Faro 2.4.7","published_at":"2026-08-05T12:00:00Z"}`)),
				Request:    request,
			}, nil
		}),
	}

	release := fetchLatestReleaseForVersion(context.Background(), client, mockCurrentVersion)
	if release == nil {
		t.Fatal("fetchLatestRelease returned nil for a newer stable release")
	}
	if release.Version != "2.4.7" || release.Display != "v2.4.7" {
		t.Fatalf("unexpected release version: %#v", release)
	}
	if release.URL != releasePageURL+"/tag/v2.4.7" {
		t.Fatalf("unexpected release URL: %q", release.URL)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}
