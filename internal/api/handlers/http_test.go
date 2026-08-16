package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSONLargePayloadUsesServerFraming(t *testing.T) {
	payload := map[string]string{"ciphertext": strings.Repeat("x", 256<<10)}
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		writeJSON(responseWriter, http.StatusOK, payload)
	}))
	defer server.Close()

	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.Header.Get("Content-Length") != "" {
		t.Fatalf("large JSON response unexpectedly used a fixed Content-Length: %q", response.Header.Get("Content-Length"))
	}
	var decoded map[string]string
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["ciphertext"] != payload["ciphertext"] {
		t.Fatalf("large JSON response was truncated or changed: got %d bytes, want %d", len(decoded["ciphertext"]), len(payload["ciphertext"]))
	}
}
