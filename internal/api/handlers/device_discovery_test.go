package handlers

import "testing"

func TestInferDeviceTypeIgnoresGenericVendorTraffic(t *testing.T) {
	typ, confidence := inferDeviceTypeFromSignals("", "192.168.1.20", []string{
		"www.googleapis.com", "api.apple.com", "login.microsoftonline.com", "plex.tv",
	})
	if typ != "Unknown" || confidence != "unknown" {
		t.Fatalf("generic traffic inferred %s (%s), want Unknown", typ, confidence)
	}
}

func TestInferDeviceTypeUsesDistinctiveSignals(t *testing.T) {
	tests := []struct {
		name    string
		domains []string
		want    string
	}{
		{name: "android", domains: []string{"connectivitycheck.gstatic.com", "mtalk.google.com"}, want: "Android Device"},
		{name: "apple", domains: []string{"mesu.apple.com", "gdmf.apple.com"}, want: "Apple Device"},
		{name: "windows", domains: []string{"www.msftconnecttest.com"}, want: "Windows PC"},
		{name: "tesla", domains: []string{"owner-api.teslamotors.com"}, want: "Tesla"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, confidence := inferDeviceTypeFromSignals("", "192.168.1.20", test.domains)
			if got != test.want || confidence == "unknown" {
				t.Fatalf("inferred %s (%s), want %s", got, confidence, test.want)
			}
		})
	}
}

func TestInferDeviceTypePrefersExplicitDeviceName(t *testing.T) {
	typ, confidence := inferDeviceTypeFromSignals("Office Synology NAS", "192.168.1.10", []string{"icloud.com"})
	if typ != "NAS" || confidence != "high" {
		t.Fatalf("inferred %s (%s), want high-confidence NAS", typ, confidence)
	}
}

func TestInferDeviceTypePrefersExplicitTVNameOverBackgroundProbe(t *testing.T) {
	typ, confidence := inferDeviceTypeFromSignals("living-room-tv", "192.168.1.11", []string{"connectivitycheck.gstatic.com"})
	if typ != "Smart TV" || confidence != "high" {
		t.Fatalf("inferred %s (%s), want high-confidence Smart TV", typ, confidence)
	}
}

func TestInferDeviceTypeRecognizesExplicitAppleTVName(t *testing.T) {
	typ, confidence := inferDeviceTypeFromSignals("bedroom-apple-tv", "192.168.1.12", []string{"mesu.apple.com"})
	if typ != "Apple TV" || confidence != "high" {
		t.Fatalf("inferred %s (%s), want high-confidence Apple TV", typ, confidence)
	}
}

func TestFriendlyHostnameRemovesLocalSuffix(t *testing.T) {
	if got := friendlyHostname("living-room-tv.home.arpa."); got != "living-room-tv" {
		t.Fatalf("friendly hostname = %q", got)
	}
}
