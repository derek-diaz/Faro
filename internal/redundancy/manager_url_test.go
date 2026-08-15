package redundancy

import (
	"context"
	"testing"
)

func TestNormalizeControllerURLAcceptsLANAddressForms(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "bare IP", raw: "192.168.7.228", want: "http://192.168.7.228:1787"},
		{name: "bare IP with port", raw: "192.168.7.228:1900", want: "http://192.168.7.228:1900"},
		{name: "URL without port", raw: "https://192.168.7.228/", want: "https://192.168.7.228:1787"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeControllerURL(context.Background(), test.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("normalized controller URL = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeControllerURLRejectsPaths(t *testing.T) {
	if _, err := normalizeControllerURL(context.Background(), "192.168.7.228/faro"); err == nil {
		t.Fatal("controller URL with a path was accepted")
	}
}
