package redundancy

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"net/http"
	"testing"
	"time"
)

func TestPairingCodeAndDerivedKeys(t *testing.T) {
	controller, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	replica, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	token := bytes.Repeat([]byte{0x24}, 32)
	code := encodePairingCode("0123456789abcdef", token, controller.PublicKey().Bytes())
	parsed, err := parsePairingCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ID != "0123456789abcdef" || !bytes.Equal(parsed.Token, token) {
		t.Fatalf("unexpected parsed pairing code: %#v", parsed)
	}

	controllerKey, err := pairingKey(controller.Bytes(), replica.PublicKey().Bytes(), token)
	if err != nil {
		t.Fatal(err)
	}
	replicaKey, err := pairingKey(replica.Bytes(), parsed.ControllerKey, parsed.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(controllerKey, replicaKey) {
		t.Fatal("controller and replica derived different pairing keys")
	}

	envelope, err := sealEnvelope(controllerKey, []byte("paired"), "pair-test")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := openEnvelope(replicaKey, envelope, "pair-test")
	if err != nil || string(plaintext) != "paired" {
		t.Fatalf("open envelope = %q, %v", plaintext, err)
	}
	if _, err := openEnvelope(replicaKey, envelope, "different-purpose"); err == nil {
		t.Fatal("envelope was accepted for a different purpose")
	}
}

func TestSignedRequestCoversPathQueryAndBody(t *testing.T) {
	secret := bytes.Repeat([]byte{0x55}, 32)
	body := []byte(`{"revision":2}`)
	request, err := http.NewRequest(http.MethodPost, "http://faro/api/redundancy/replica/ack?node=1", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	if err := signRequest(request, body, secret, now); err != nil {
		t.Fatal(err)
	}
	signature, err := decodeSignature(request.Header.Get("X-Faro-Signature"))
	if err != nil {
		t.Fatal(err)
	}
	expected := requestSignature(
		secret,
		request.Method,
		request.URL.RequestURI(),
		request.Header.Get("X-Faro-Timestamp"),
		request.Header.Get("X-Faro-Nonce"),
		body,
	)
	if !bytes.Equal(signature, expected) {
		t.Fatal("signed request did not verify")
	}
	tampered := requestSignature(secret, request.Method, request.URL.RequestURI(), request.Header.Get("X-Faro-Timestamp"), request.Header.Get("X-Faro-Nonce"), []byte(`{"revision":3}`))
	if bytes.Equal(signature, tampered) {
		t.Fatal("request signature did not cover the body")
	}
}
