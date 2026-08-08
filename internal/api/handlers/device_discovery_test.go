package handlers

import "testing"

func TestFriendlyHostnameRemovesLocalSuffix(t *testing.T) {
	if got := friendlyHostname("living-room-tv.home.arpa."); got != "living-room-tv" {
		t.Fatalf("friendly hostname = %q", got)
	}
}
