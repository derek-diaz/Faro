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
		{candidate: "v0.9.1", current: "0.9.0", want: true},
		{candidate: "1.0.0", current: "v0.9.0", want: true},
		{candidate: "0.9.0", current: "0.9.0", want: false},
		{candidate: "0.8.9", current: "0.9.0", want: false},
		{candidate: "latest", current: "0.9.0", want: false},
	}
	for _, test := range tests {
		if got := IsNewer(test.candidate, test.current); got != test.want {
			t.Errorf("IsNewer(%q, %q) = %t, want %t", test.candidate, test.current, got, test.want)
		}
	}
}

func TestFetchLatestRelease(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"tag_name":"v0.9.1","name":"Faro 0.9.1","published_at":"2026-08-05T12:00:00Z"}`)),
				Request:    request,
			}, nil
		}),
	}

	release := fetchLatestRelease(context.Background(), client)
	if release == nil {
		t.Fatal("fetchLatestRelease returned nil for a newer stable release")
	}
	if release.Version != "0.9.1" || release.Display != "v0.9.1" {
		t.Fatalf("unexpected release version: %#v", release)
	}
	if release.URL != releasePageURL+"/tag/v0.9.1" {
		t.Fatalf("unexpected release URL: %q", release.URL)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
